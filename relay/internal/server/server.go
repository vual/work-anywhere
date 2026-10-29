package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	relayv1 "github.com/vual/work-anywhere/relay/gen/waw/relay/v1"
	"github.com/vual/work-anywhere/relay/internal/config"
	"github.com/vual/work-anywhere/relay/internal/jobs"
	"github.com/vual/work-anywhere/relay/internal/limits"
	"github.com/vual/work-anywhere/relay/internal/observability"
	waProtocol "github.com/vual/work-anywhere/relay/internal/protocol"
	"github.com/vual/work-anywhere/relay/internal/routing"
	"github.com/vual/work-anywhere/relay/internal/secretbox"
	"github.com/vual/work-anywhere/relay/internal/store"
)

type Store interface {
	Ping(context.Context) error
	Enroll(context.Context, store.EnrollmentInput, bool) (store.Device, error)
	RegisterDaemon(context.Context, store.EnrollmentInput, string, int, bool) (store.Device, bool, error)
	Device(context.Context, uuid.UUID) (store.Device, error)
	TouchDevice(context.Context, uuid.UUID)
	ActivePairing(context.Context, uuid.UUID, uuid.UUID) (uuid.UUID, []string, error)
	CreatePairingSession(context.Context, uuid.UUID, uuid.UUID, time.Time) error
	CancelPairingSession(context.Context, uuid.UUID, uuid.UUID) error
	ClaimPairingAttempt(context.Context, uuid.UUID) (store.PairingSession, error)
	CompletePairing(context.Context, store.PairingSession, store.MobileEnrollment, []string, []byte) (uuid.UUID, error)
	RevokePairing(context.Context, uuid.UUID, uuid.UUID) (uuid.UUID, uuid.UUID, error)
	UpsertPushEndpoint(context.Context, store.PushEndpoint) (uuid.UUID, error)
	DeletePushEndpoint(context.Context, uuid.UUID, string, string, string) error
	ActivePushEndpointIDs(context.Context, uuid.UUID) ([]uuid.UUID, error)
}

type PushQueue interface {
	Enqueue(context.Context, []uuid.UUID, jobs.Notification) error
}

type Option func(*Server)

func WithPushQueue(queue PushQueue) Option { return func(server *Server) { server.push = queue } }

type Server struct {
	cfg          config.Config
	store        Store
	registry     *routing.Registry
	acl          *routing.ACLCache
	limits       *limits.Limiter
	box          *secretbox.Box
	push         PushQueue
	metrics      *observability.Metrics
	logger       *slog.Logger
	ready        atomic.Bool
	http         *http.Server
	pairMu       sync.Mutex
	pairs        map[uuid.UUID]*pendingPair
	enrollmentMu sync.Mutex
	enrollments  map[uuid.UUID]pendingEnrollment
}

type pendingPair struct {
	session   store.PairingSession
	mobile    store.MobileEnrollment
	challenge []byte
	result    chan *relayv1.PairResult
}

func New(cfg config.Config, state Store, logger *slog.Logger, options ...Option) *Server {
	server := &Server{cfg: cfg, store: state, registry: routing.NewRegistry(cfg.MaxConnections), acl: routing.NewACLCache(cfg.ACLCacheTTL), limits: limits.New(100_000), logger: logger, pairs: make(map[uuid.UUID]*pendingPair), metrics: &observability.Metrics{}}
	server.box, _ = secretbox.New(cfg.DataKey)
	server.enrollments = make(map[uuid.UUID]pendingEnrollment)
	for _, option := range options {
		option(server)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", server.live)
	mux.HandleFunc("GET /health/ready", server.readiness)
	mux.HandleFunc("GET /metrics", server.prometheus)
	mux.HandleFunc("POST /v1/enroll/daemon", server.enrollDaemon)
	mux.HandleFunc("GET /v1/enrollment/policy", server.enrollmentPolicy)
	mux.HandleFunc("POST /v1/enrollment/challenges", server.enrollmentChallenge)
	mux.HandleFunc("POST /v1/enrollment/complete", server.completeEnrollment)
	mux.HandleFunc("GET /v1/connect", server.connect)
	mux.HandleFunc("GET /v1/pair", server.pair)
	server.http = &http.Server{Addr: cfg.ListenAddr, Handler: securityHeaders(mux), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 90 * time.Second}
	return server
}

func (s *Server) Run(ctx context.Context) error {
	go s.cleanEnrollments(ctx)
	s.ready.Store(true)
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("relay.started", "listen_addr", s.cfg.ListenAddr)
		errCh <- s.http.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		s.ready.Store(false)
		s.registry.CloseAll()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		defer cancel()
		if err := s.http.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) Handler() http.Handler { return s.http.Handler }

func (s *Server) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) readiness(w http.ResponseWriter, r *http.Request) {
	if !s.ready.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "code": "starting"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "code": "database_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "connections": s.registry.Count()})
}

func (s *Server) prometheus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	s.metrics.WritePrometheus(w, s.registry.Count())
}

type enrollmentRequest struct {
	Token                  string `json:"token"`
	DisplayName            string `json:"displayName"`
	Ed25519PublicKeyBase64 string `json:"ed25519PublicKey"`
	X25519PublicKeyBase64  string `json:"x25519PublicKey"`
	CapabilityHashBase64   string `json:"capabilityHash"`
}

func (s *Server) enrollDaemon(w http.ResponseWriter, r *http.Request) {
	if !s.limits.Allow("enroll:"+s.peerIP(r), 0.2, 5) {
		s.metrics.EnrollmentRateLimited.Add(1)
		w.Header().Set("Retry-After", "5")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "rate_limited"})
		return
	}
	if s.cfg.EnrollmentMode == "disabled" || s.cfg.EnrollmentMode == "self_service" {
		s.enrollmentError(w, store.EnrollmentError("enrollment_disabled"))
		return
	}
	var request enrollmentRequest
	if !s.decodeEnrollment(w, r, &request) {
		return
	}
	edKey, err := decodeBase64(request.Ed25519PublicKeyBase64)
	if err != nil || len(edKey) != ed25519.PublicKeySize {
		s.enrollmentError(w, store.EnrollmentError("invalid_request"))
		return
	}
	xKey, err := decodeBase64(request.X25519PublicKeyBase64)
	if err != nil || len(xKey) != 32 {
		s.enrollmentError(w, store.EnrollmentError("invalid_request"))
		return
	}
	capabilityHash, err := decodeBase64(request.CapabilityHashBase64)
	if err != nil || len(capabilityHash) != 32 {
		s.enrollmentError(w, store.EnrollmentError("invalid_request"))
		return
	}
	device, _, err := s.store.RegisterDaemon(r.Context(), store.EnrollmentInput{Token: request.Token, Role: int16(relayv1.DeviceRole_DEVICE_ROLE_DAEMON), DisplayName: request.DisplayName, Ed25519PublicKey: edKey, X25519PublicKey: xKey, CapabilityHash: capabilityHash}, s.cfg.EnrollmentMode, s.cfg.MaxDaemons, false)
	if err != nil {
		s.enrollmentError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"deviceId": device.ID.String()})
	s.metrics.EnrollmentSuccess.Add(1)
	s.metrics.EnrollmentCreated.Add(1)
}

func (s *Server) pair(w http.ResponseWriter, r *http.Request) {
	if !s.limits.Allow("pair:"+s.peerIP(r), 0.5, 10) {
		w.Header().Set("Retry-After", "2")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "rate_limited"})
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	ws.SetReadLimit(64 << 10)
	defer ws.CloseNow()
	connectionID, _ := waProtocol.NewID()
	challenge := make([]byte, 32)
	if _, err = rand.Read(challenge); err != nil {
		return
	}
	expiresAt := time.Now().Add(30 * time.Second).UnixMilli()
	if err = writeProto(r.Context(), ws, &relayv1.ServerFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: mustID(), Body: &relayv1.ServerFrame_ServerChallenge{ServerChallenge: &relayv1.ServerChallenge{ConnectionId: connectionID, Challenge: challenge, ServerTimeMs: time.Now().UnixMilli(), ExpiresAtMs: expiresAt}}}); err != nil {
		return
	}
	readCtx, cancel := context.WithDeadline(r.Context(), time.UnixMilli(expiresAt))
	defer cancel()
	kind, raw, err := ws.Read(readCtx)
	if err != nil || kind != websocket.MessageBinary {
		return
	}
	var frame relayv1.ClientFrame
	if proto.Unmarshal(raw, &frame) != nil || frame.WireMajor != waProtocol.WireMajor || waProtocol.ValidateID(frame.MessageId) != nil || frame.GetPairJoin() == nil {
		_ = writeProto(r.Context(), ws, protocolError("protocol_violation", "valid pair_join required", frame.MessageId))
		return
	}
	join := frame.GetPairJoin()
	pairingID, err := uuid.FromBytes(join.PairingId)
	if err != nil || len(join.MobileEd25519PublicKey) != 32 || len(join.MobileX25519PublicKey) != 32 || len(join.Proof) != 32 || len(join.CapabilityHash) == 0 || len(join.CapabilityHash) > 64 {
		_ = writeProto(r.Context(), ws, protocolError("protocol_violation", "invalid pairing fields", frame.MessageId))
		return
	}
	session, err := s.store.ClaimPairingAttempt(r.Context(), pairingID)
	if err != nil {
		_ = writeProto(r.Context(), ws, protocolError("pairing_expired", "pairing is unavailable", frame.MessageId))
		return
	}
	daemon, online := s.registry.Get(session.DaemonDeviceID)
	if !online {
		_ = writeProto(r.Context(), ws, protocolError("peer_offline", "daemon is offline", frame.MessageId))
		return
	}
	pending := &pendingPair{session: session, mobile: store.MobileEnrollment{ID: uuid.New(), DisplayName: join.DisplayName, Ed25519PublicKey: append([]byte(nil), join.MobileEd25519PublicKey...), X25519PublicKey: append([]byte(nil), join.MobileX25519PublicKey...), CapabilityHash: append([]byte(nil), join.CapabilityHash...)}, challenge: append([]byte(nil), challenge...), result: make(chan *relayv1.PairResult, 1)}
	s.pairMu.Lock()
	if s.pairs[pairingID] != nil {
		s.pairMu.Unlock()
		_ = writeProto(r.Context(), ws, protocolError("temporarily_unavailable", "pairing already has an active attempt", frame.MessageId))
		return
	}
	s.pairs[pairingID] = pending
	s.pairMu.Unlock()
	defer func() {
		s.pairMu.Lock()
		if s.pairs[pairingID] == pending {
			delete(s.pairs, pairingID)
		}
		s.pairMu.Unlock()
	}()
	hello := &relayv1.ServerFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: frame.MessageId, Body: &relayv1.ServerFrame_PairHello{PairHello: &relayv1.PairHello{PairingId: pairingID[:], ConnectionId: connectionID, Challenge: challenge, MobileEd25519PublicKey: pending.mobile.Ed25519PublicKey, MobileX25519PublicKey: pending.mobile.X25519PublicKey, DisplayName: pending.mobile.DisplayName, CapabilityHash: pending.mobile.CapabilityHash, Proof: join.Proof, CandidateMobileDeviceId: pending.mobile.ID[:]}}}
	if err = s.enqueue(daemon, hello); err != nil {
		_ = writeProto(r.Context(), ws, protocolError("queue_full", "daemon cannot receive pairing request", frame.MessageId))
		return
	}
	waitUntil := session.ExpiresAt
	if maximum := time.Now().Add(time.Minute); waitUntil.After(maximum) {
		waitUntil = maximum
	}
	timer := time.NewTimer(time.Until(waitUntil))
	defer timer.Stop()
	select {
	case result := <-pending.result:
		_ = writeProto(r.Context(), ws, &relayv1.ServerFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: frame.MessageId, Body: &relayv1.ServerFrame_PairResult{PairResult: result}})
	case <-timer.C:
		_ = writeProto(r.Context(), ws, protocolError("pairing_expired", "pairing decision timed out", frame.MessageId))
	case <-r.Context().Done():
	}
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	if !s.limits.Allow("connect:"+s.peerIP(r), 1, 20) {
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "rate_limited"})
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	ws.SetReadLimit(waProtocol.MaxFrameBytes)
	defer ws.CloseNow()
	authenticated := false
	defer func() {
		if !authenticated {
			s.metrics.AuthFailure.Add(1)
		}
	}()

	connectionID, _ := waProtocol.NewID()
	challenge := make([]byte, 32)
	if _, err = rand.Read(challenge); err != nil {
		return
	}
	expiresAt := time.Now().Add(30 * time.Second).UnixMilli()
	challengeFrame := &relayv1.ServerFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: mustID(), Body: &relayv1.ServerFrame_ServerChallenge{ServerChallenge: &relayv1.ServerChallenge{ConnectionId: connectionID, Challenge: challenge, ServerTimeMs: time.Now().UnixMilli(), ExpiresAtMs: expiresAt}}}
	if err = writeProto(r.Context(), ws, challengeFrame); err != nil {
		return
	}

	authCtx, cancel := context.WithDeadline(r.Context(), time.UnixMilli(expiresAt))
	defer cancel()
	kind, raw, err := ws.Read(authCtx)
	if err != nil || kind != websocket.MessageBinary {
		return
	}
	var frame relayv1.ClientFrame
	if err = proto.Unmarshal(raw, &frame); err != nil || frame.WireMajor != waProtocol.WireMajor || frame.GetClientAuth() == nil {
		_ = writeProto(r.Context(), ws, protocolError("protocol_violation", "valid client_auth required", frame.MessageId))
		return
	}
	auth := frame.GetClientAuth()
	deviceID, err := uuid.FromBytes(auth.DeviceId)
	if err != nil || len(auth.Signature) != ed25519.SignatureSize || time.Now().UnixMilli() > expiresAt {
		_ = writeProto(r.Context(), ws, protocolError("auth_failed", "device authentication failed", frame.MessageId))
		return
	}
	device, err := s.store.Device(r.Context(), deviceID)
	if err != nil && !errors.Is(err, store.ErrDeviceNotFound) {
		_ = writeProto(r.Context(), ws, protocolError("temporarily_unavailable", "device lookup temporarily unavailable", frame.MessageId))
		return
	}
	if err != nil || device.Status != "active" || int32(device.Role) != int32(auth.Role) || subtle.ConstantTimeCompare(device.CapabilityHash, auth.CapabilityHash) != 1 || !ed25519.Verify(device.Ed25519PublicKey, waProtocol.AuthSignatureInput(connectionID, challenge, expiresAt, auth.CapabilityHash), auth.Signature) {
		_ = writeProto(r.Context(), ws, protocolError("auth_failed", "device authentication failed", frame.MessageId))
		return
	}

	connection := routing.NewConnection(r.Context(), deviceID, device.Role, routing.DefaultQueueFrames, routing.DefaultQueueBytes)
	if _, err = s.registry.Register(connection); err != nil {
		_ = writeProto(r.Context(), ws, protocolError("temporarily_unavailable", "relay connection capacity reached", frame.MessageId))
		return
	}
	defer s.registry.Unregister(connection)
	go s.store.TouchDevice(context.Background(), deviceID)
	if err = writeProto(r.Context(), ws, &relayv1.ServerFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: frame.MessageId, Body: &relayv1.ServerFrame_AuthOk{AuthOk: &relayv1.AuthOk{DeviceId: deviceID[:], ConnectionEpoch: connection.Epoch, ServerTimeMs: time.Now().UnixMilli()}}}); err != nil {
		return
	}
	authenticated = true
	s.metrics.AuthSuccess.Add(1)

	writerDone := make(chan error, 1)
	go func() { writerDone <- s.writeLoop(ws, connection) }()
	readDone := make(chan error, 1)
	go func() { readDone <- s.readLoop(ws, connection) }()
	select {
	case <-connection.Context().Done():
	case <-writerDone:
	case <-readDone:
	}
	connection.Close()
}

func (s *Server) writeLoop(ws *websocket.Conn, connection *routing.Connection) error {
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-connection.Context().Done():
			return connection.Context().Err()
		case raw := <-connection.Outbound:
			ctx, cancel := context.WithTimeout(connection.Context(), 10*time.Second)
			err := ws.Write(ctx, websocket.MessageBinary, raw)
			cancel()
			connection.Dequeued(raw)
			if err != nil {
				return err
			}
		case <-ping.C:
			ctx, cancel := context.WithTimeout(connection.Context(), 10*time.Second)
			err := ws.Ping(ctx)
			cancel()
			if err != nil {
				return err
			}
		}
	}
}

func (s *Server) readLoop(ws *websocket.Conn, connection *routing.Connection) error {
	for {
		if !s.limits.Allow("frame:"+connection.DeviceID.String(), 120, 240) {
			return errors.New("device frame rate exceeded")
		}
		kind, raw, err := ws.Read(connection.Context())
		if err != nil {
			return err
		}
		if kind != websocket.MessageBinary {
			return errors.New("text frames are not accepted")
		}
		var frame relayv1.ClientFrame
		if err = proto.Unmarshal(raw, &frame); err != nil {
			return err
		}
		if frame.WireMajor != waProtocol.WireMajor || waProtocol.ValidateID(frame.MessageId) != nil {
			_ = s.enqueue(connection, protocolError("unsupported_version", "unsupported or malformed frame", frame.MessageId))
			continue
		}
		if frame.GetPong() != nil {
			continue
		}
		if start := frame.GetPairStart(); start != nil {
			if err = s.startPairing(connection, frame.MessageId, start); err != nil {
				_ = s.enqueue(connection, protocolError("invalid_pairing", "pairing session could not be created", frame.MessageId))
			}
			continue
		}
		if cancel := frame.GetPairCancel(); cancel != nil {
			if err = s.cancelPairing(connection, cancel); err != nil {
				_ = s.enqueue(connection, routeAck(frame.MessageId, false, "pairing_not_found"))
			} else {
				_ = s.enqueue(connection, routeAck(frame.MessageId, true, ""))
			}
			continue
		}
		if decision := frame.GetPairDecision(); decision != nil {
			if err = s.completePairing(connection, decision); err != nil {
				_ = s.enqueue(connection, routeAck(frame.MessageId, false, "invalid_pairing_decision"))
			} else {
				_ = s.enqueue(connection, routeAck(frame.MessageId, true, ""))
			}
			continue
		}
		if revoke := frame.GetPairRevoke(); revoke != nil {
			if err = s.revokePairing(connection, frame.MessageId, revoke); err != nil {
				_ = s.enqueue(connection, routeAck(frame.MessageId, false, "pairing_not_found"))
			}
			continue
		}
		if register := frame.GetPushRegister(); register != nil {
			if err = s.registerPush(connection, register); err != nil {
				_ = s.enqueue(connection, routeAck(frame.MessageId, false, "invalid_push_endpoint"))
			} else {
				_ = s.enqueue(connection, routeAck(frame.MessageId, true, ""))
			}
			continue
		}
		if unregister := frame.GetPushUnregister(); unregister != nil {
			if err = s.unregisterPush(connection, unregister); err != nil {
				_ = s.enqueue(connection, routeAck(frame.MessageId, false, "push_endpoint_not_found"))
			} else {
				_ = s.enqueue(connection, routeAck(frame.MessageId, true, ""))
			}
			continue
		}
		if notification := frame.GetNotify(); notification != nil {
			if err = s.notify(connection, notification); err != nil {
				_ = s.enqueue(connection, routeAck(frame.MessageId, false, "notification_rejected"))
			} else {
				_ = s.enqueue(connection, routeAck(frame.MessageId, true, ""))
			}
			continue
		}
		if err = s.route(connection, &frame); err != nil {
			s.metrics.RouteFailure.Add(1)
			code := "temporarily_unavailable"
			switch {
			case errors.Is(err, errPayloadTooLarge):
				code = "payload_too_large"
			case errors.Is(err, errPeerOffline):
				code = "peer_offline"
			case errors.Is(err, routing.ErrQueueFull):
				code = "queue_full"
			}
			_ = s.enqueue(connection, routeAck(frame.MessageId, false, code))
			continue
		}
		s.metrics.FramesRouted.Add(1)
		_ = s.enqueue(connection, routeAck(frame.MessageId, true, ""))
	}
}

func (s *Server) registerPush(connection *routing.Connection, request *relayv1.PushRegister) error {
	if connection.Role != int16(relayv1.DeviceRole_DEVICE_ROLE_MOBILE) || s.box == nil {
		return errors.New("push registration requires an authenticated mobile")
	}
	if (request.Platform != "apns" && request.Platform != "fcm") || len(request.AppId) == 0 || len(request.AppId) > 255 || len(request.Environment) == 0 || len(request.Environment) > 32 || len(request.Locale) > 32 || request.Generation == 0 || len(request.ProviderToken) < 16 || len(request.ProviderToken) > 4096 {
		return errors.New("invalid push endpoint")
	}
	if request.Platform == "apns" && request.Environment != "sandbox" && request.Environment != "production" {
		return errors.New("invalid APNs environment")
	}
	if request.Platform == "fcm" && request.Environment != "production" && request.Environment != "test" {
		return errors.New("invalid FCM environment")
	}
	aad := secretbox.AssociatedData(connection.DeviceID.String(), request.Platform, request.AppId, request.Environment)
	ciphertext, err := s.box.Seal(request.ProviderToken, aad)
	if err != nil {
		return err
	}
	_, err = s.store.UpsertPushEndpoint(connection.Context(), store.PushEndpoint{MobileDeviceID: connection.DeviceID, Platform: request.Platform, AppID: request.AppId, Environment: request.Environment, Locale: request.Locale, TokenCiphertext: ciphertext, Generation: int64(request.Generation)})
	return err
}

func (s *Server) unregisterPush(connection *routing.Connection, request *relayv1.PushUnregister) error {
	if connection.Role != int16(relayv1.DeviceRole_DEVICE_ROLE_MOBILE) {
		return errors.New("push unregister requires an authenticated mobile")
	}
	return s.store.DeletePushEndpoint(connection.Context(), connection.DeviceID, request.Platform, request.AppId, request.Environment)
}

func (s *Server) notify(connection *routing.Connection, request *relayv1.Notify) error {
	if connection.Role != int16(relayv1.DeviceRole_DEVICE_ROLE_DAEMON) || s.push == nil || !s.limits.Allow("notify:"+connection.DeviceID.String(), 1, 5) {
		return errors.New("notification unavailable")
	}
	pairingID, err := uuid.FromBytes(request.PairingId)
	if err != nil || len(request.NotificationId) != waProtocol.MessageIDBytes || len(request.CollapseKey) > 64 {
		return errors.New("invalid notification")
	}
	recipient, err := uuid.FromBytes(request.RecipientDeviceId)
	if err != nil {
		return err
	}
	activeID, _, err := s.store.ActivePairing(connection.Context(), connection.DeviceID, recipient)
	if err != nil || activeID != pairingID {
		return errors.New("notification recipient is not paired")
	}
	expiresAt := time.UnixMilli(request.ExpiresAtMs)
	if expiresAt.Before(time.Now()) || expiresAt.After(time.Now().Add(time.Hour)) {
		return errors.New("invalid notification expiry")
	}
	hint := strings.TrimPrefix(strings.ToLower(request.Hint.String()), "notification_hint_")
	if hint != "generic" && hint != "action_required" && hint != "turn_completed" {
		return errors.New("invalid notification hint")
	}
	endpointIDs, err := s.store.ActivePushEndpointIDs(connection.Context(), recipient)
	if err != nil {
		return err
	}
	err = s.push.Enqueue(connection.Context(), endpointIDs, jobs.Notification{ID: base64.RawURLEncoding.EncodeToString(request.NotificationId), Hint: hint, CollapseKey: request.CollapseKey, ExpiresAt: expiresAt})
	if err != nil {
		s.metrics.NotificationFailure.Add(1)
		return err
	}
	s.metrics.NotificationsQueued.Add(1)
	return nil
}

func (s *Server) revokePairing(connection *routing.Connection, messageID []byte, revoke *relayv1.PairRevoke) error {
	pairingID, err := uuid.FromBytes(revoke.PairingId)
	if err != nil {
		return err
	}
	daemonID, mobileID, err := s.store.RevokePairing(connection.Context(), pairingID, connection.DeviceID)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	s.acl.InvalidatePairing(pairingID)
	s.metrics.PairingRevoked.Add(1)
	result := &relayv1.ServerFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: messageID, Body: &relayv1.ServerFrame_PairRevoked{PairRevoked: &relayv1.PairRevoked{PairingId: pairingID[:], RevokedAtMs: now}}}
	for _, id := range []uuid.UUID{daemonID, mobileID} {
		if target, ok := s.registry.Get(id); ok {
			_ = s.enqueue(target, result)
		}
	}
	return nil
}

func (s *Server) startPairing(connection *routing.Connection, messageID []byte, start *relayv1.PairStart) error {
	if connection.Role != int16(relayv1.DeviceRole_DEVICE_ROLE_DAEMON) {
		return errors.New("only daemon devices can start pairing")
	}
	pairingID, err := uuid.FromBytes(start.PairingId)
	if err != nil {
		return err
	}
	now := time.Now()
	expiresAt := time.UnixMilli(start.ExpiresAtMs)
	if expiresAt.Before(now.Add(10*time.Second)) || expiresAt.After(now.Add(5*time.Minute)) {
		return errors.New("pairing expiry is outside the allowed window")
	}
	if err = s.store.CreatePairingSession(connection.Context(), pairingID, connection.DeviceID, expiresAt); err != nil {
		return err
	}
	return s.enqueue(connection, &relayv1.ServerFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: messageID, Body: &relayv1.ServerFrame_PairStarted{PairStarted: &relayv1.PairStarted{PairingId: pairingID[:], ExpiresAtMs: expiresAt.UnixMilli()}}})
}

func (s *Server) cancelPairing(connection *routing.Connection, cancel *relayv1.PairCancel) error {
	if connection.Role != int16(relayv1.DeviceRole_DEVICE_ROLE_DAEMON) {
		return errors.New("only daemon devices can cancel pairing")
	}
	pairingID, err := uuid.FromBytes(cancel.PairingId)
	if err != nil {
		return err
	}
	if err = s.store.CancelPairingSession(connection.Context(), pairingID, connection.DeviceID); err != nil {
		return err
	}
	s.pairMu.Lock()
	pending := s.pairs[pairingID]
	s.pairMu.Unlock()
	if pending != nil {
		select {
		case pending.result <- &relayv1.PairResult{PairingId: pairingID[:], Approved: false, ErrorCode: "cancelled"}:
		default:
		}
	}
	return nil
}

func (s *Server) completePairing(connection *routing.Connection, decision *relayv1.PairDecision) error {
	if connection.Role != int16(relayv1.DeviceRole_DEVICE_ROLE_DAEMON) {
		return errors.New("only daemon devices can resolve pairing")
	}
	pairingID, err := uuid.FromBytes(decision.PairingId)
	if err != nil {
		return err
	}
	s.pairMu.Lock()
	pending := s.pairs[pairingID]
	s.pairMu.Unlock()
	if pending == nil || pending.session.DaemonDeviceID != connection.DeviceID {
		return errors.New("pairing attempt is not pending")
	}
	if !decision.Approved {
		select {
		case pending.result <- &relayv1.PairResult{PairingId: pairingID[:], Approved: false, ErrorCode: "rejected"}:
		default:
		}
		return nil
	}
	if !equalUUIDBytes(pending.mobile.ID, decision.MobileDeviceId) || decision.GrantExpiresAtMs < time.Now().UnixMilli() || decision.GrantExpiresAtMs > pending.session.ExpiresAt.UnixMilli() || !validScopes(decision.Scopes) {
		return errors.New("invalid pairing grant fields")
	}
	device, err := s.store.Device(connection.Context(), connection.DeviceID)
	if err != nil {
		return err
	}
	signatureInput := waProtocol.PairGrantSignatureInput(pairingID[:], connection.DeviceID[:], pending.mobile.ID[:], pending.mobile.Ed25519PublicKey, pending.mobile.X25519PublicKey, decision.Scopes, decision.GrantExpiresAtMs)
	if len(decision.GrantSignature) != ed25519.SignatureSize || !ed25519.Verify(device.Ed25519PublicKey, signatureInput, decision.GrantSignature) {
		return errors.New("invalid pairing grant signature")
	}
	activePairingID, err := s.store.CompletePairing(connection.Context(), pending.session, pending.mobile, decision.Scopes, decision.GrantSignature)
	if err != nil {
		s.metrics.PairingFailure.Add(1)
		return err
	}
	s.metrics.PairingSuccess.Add(1)
	result := &relayv1.PairResult{PairingId: activePairingID[:], Approved: true, DaemonDeviceId: connection.DeviceID[:], MobileDeviceId: pending.mobile.ID[:], Scopes: append([]string(nil), decision.Scopes...), DaemonEd25519PublicKey: device.Ed25519PublicKey, DaemonX25519PublicKey: device.X25519PublicKey, GrantSignature: append([]byte(nil), decision.GrantSignature...), GrantExpiresAtMs: decision.GrantExpiresAtMs}
	select {
	case pending.result <- result:
	default:
		return errors.New("pairing client is no longer waiting")
	}
	return s.enqueue(connection, &relayv1.ServerFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: mustID(), Body: &relayv1.ServerFrame_PairResult{PairResult: result}})
}

func validScopes(scopes []string) bool {
	allowed := map[string]bool{
		"conversation.create": true,
		"status.read":         true, "conversation.read": true, "turn.start": true, "turn.interrupt": true,
		"action.read": true, "action.resolve": true, "attachment.write": true, "catalog.read": true,
	}
	if len(scopes) == 0 || len(scopes) > len(allowed) {
		return false
	}
	ordered := append([]string(nil), scopes...)
	sort.Strings(ordered)
	for index, scope := range ordered {
		if !allowed[scope] || (index > 0 && ordered[index-1] == scope) {
			return false
		}
	}
	return true
}

func (s *Server) route(sender *routing.Connection, frame *relayv1.ClientFrame) error {
	recipientBytes, pairingBytes, forwarded, err := forwardedFrame(sender.DeviceID, frame)
	if err != nil {
		return err
	}
	recipient, err := uuid.FromBytes(recipientBytes)
	if err != nil || recipient == sender.DeviceID {
		return errors.New("invalid recipient")
	}
	pairingID, err := uuid.FromBytes(pairingBytes)
	if err != nil {
		return errors.New("invalid pairing id")
	}
	acl, ok := s.acl.Get(sender.DeviceID, recipient)
	if !ok {
		activePairingID, scopes, lookupErr := s.store.ActivePairing(sender.Context(), sender.DeviceID, recipient)
		if lookupErr != nil {
			return errors.New("not paired")
		}
		s.acl.Put(sender.DeviceID, recipient, activePairingID, scopes)
		acl.PairingID = activePairingID
	}
	if acl.PairingID != pairingID {
		return errors.New("not paired")
	}
	target, ok := s.registry.Get(recipient)
	if !ok {
		return errPeerOffline
	}
	raw, err := proto.Marshal(forwarded)
	if err != nil {
		return err
	}
	if err := target.Enqueue(raw); err != nil {
		if errors.Is(err, routing.ErrQueueFull) {
			target.Close()
		}
		return err
	}
	return nil
}

var (
	errPayloadTooLarge = errors.New("payload too large")
	errPeerOffline     = errors.New("peer offline")
)

func peerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func forwardedFrame(sender uuid.UUID, frame *relayv1.ClientFrame) ([]byte, []byte, *relayv1.ServerFrame, error) {
	result := &relayv1.ServerFrame{WireMajor: frame.WireMajor, WireMinor: frame.WireMinor, MessageId: frame.MessageId}
	switch body := frame.Body.(type) {
	case *relayv1.ClientFrame_SessionOpen:
		body.SessionOpen.SenderDeviceId = sender[:]
		result.Body = &relayv1.ServerFrame_SessionOpen{SessionOpen: body.SessionOpen}
		return body.SessionOpen.RecipientDeviceId, body.SessionOpen.PairingId, result, nil
	case *relayv1.ClientFrame_SessionHandshake:
		body.SessionHandshake.SenderDeviceId = sender[:]
		result.Body = &relayv1.ServerFrame_SessionHandshake{SessionHandshake: body.SessionHandshake}
		return body.SessionHandshake.RecipientDeviceId, body.SessionHandshake.PairingId, result, nil
	case *relayv1.ClientFrame_SessionData:
		if len(body.SessionData.Ciphertext) > waProtocol.MaxPayloadBytes {
			return nil, nil, nil, errPayloadTooLarge
		}
		if body.SessionData.ExpiresAtMs < time.Now().UnixMilli() {
			return nil, nil, nil, errors.New("invalid session data")
		}
		body.SessionData.SenderDeviceId = sender[:]
		result.Body = &relayv1.ServerFrame_SessionData{SessionData: body.SessionData}
		return body.SessionData.RecipientDeviceId, body.SessionData.PairingId, result, nil
	case *relayv1.ClientFrame_SessionClose:
		body.SessionClose.SenderDeviceId = sender[:]
		result.Body = &relayv1.ServerFrame_SessionClose{SessionClose: body.SessionClose}
		return body.SessionClose.RecipientDeviceId, body.SessionClose.PairingId, result, nil
	case *relayv1.ClientFrame_DeliveryAck:
		body.DeliveryAck.SenderDeviceId = sender[:]
		result.Body = &relayv1.ServerFrame_DeliveryAck{DeliveryAck: body.DeliveryAck}
		return body.DeliveryAck.RecipientDeviceId, body.DeliveryAck.PairingId, result, nil
	default:
		return nil, nil, nil, errors.New("frame is not routable")
	}
}

func (s *Server) enqueue(connection *routing.Connection, frame *relayv1.ServerFrame) error {
	raw, err := proto.Marshal(frame)
	if err != nil {
		return err
	}
	return connection.Enqueue(raw)
}

func routeAck(messageID []byte, accepted bool, code string) *relayv1.ServerFrame {
	return &relayv1.ServerFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: mustID(), Body: &relayv1.ServerFrame_RouteAck{RouteAck: &relayv1.RouteAck{RoutedMessageId: messageID, Accepted: accepted, ErrorCode: code}}}
}

func protocolError(code, message string, related []byte) *relayv1.ServerFrame {
	return &relayv1.ServerFrame{WireMajor: waProtocol.WireMajor, WireMinor: waProtocol.WireMinor, MessageId: mustID(), Body: &relayv1.ServerFrame_ProtocolError{ProtocolError: &relayv1.ProtocolError{Code: code, Message: message, RelatedMessageId: related}}}
}

func writeProto(ctx context.Context, ws *websocket.Conn, frame *relayv1.ServerFrame) error {
	raw, err := proto.Marshal(frame)
	if err != nil {
		return err
	}
	return ws.Write(ctx, websocket.MessageBinary, raw)
}

func mustID() []byte {
	value, err := waProtocol.NewID()
	if err != nil {
		panic(err)
	}
	return value
}

func equalUUIDBytes(id uuid.UUID, raw []byte) bool {
	return len(raw) == len(id) && subtle.ConstantTimeCompare(id[:], raw) == 1
}

func decodeBase64(value string) ([]byte, error) {
	if data, err := base64.StdEncoding.DecodeString(value); err == nil {
		return data, nil
	}
	return base64.RawURLEncoding.DecodeString(value)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}
