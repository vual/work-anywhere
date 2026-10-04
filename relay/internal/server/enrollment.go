package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vual/work-anywhere/relay/internal/store"
	"github.com/vual/work-anywhere/relay/pkg/endpoint/protocol"
)

type pendingEnrollment struct {
	input     store.EnrollmentInput
	challenge []byte
	expiresAt int64
}

func (s *Server) enrollmentPolicy(w http.ResponseWriter, request *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"protocol": protocol.EnrollmentProtocol, "mode": s.cfg.EnrollmentMode, "relayOrigin": s.cfg.PublicURL})
}

func (s *Server) decodeEnrollment(w http.ResponseWriter, request *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		s.enrollmentError(w, store.EnrollmentError("invalid_request"))
		return false
	}
	return true
}

func (s *Server) enrollmentRate(w http.ResponseWriter, request *http.Request, operation string, burst int) bool {
	if !s.limits.Allow("enrollment:"+operation+":"+s.peerIP(request), 0.2, 5) || !s.limits.Allow("enrollment:"+operation+":global", 10, burst) {
		s.metrics.EnrollmentRateLimited.Add(1)
		w.Header().Set("Retry-After", "5")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "rate_limited"})
		return false
	}
	return true
}

func (s *Server) enrollmentChallenge(w http.ResponseWriter, request *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.enrollmentRate(w, request, "challenge", 50) {
		return
	}
	var input enrollmentRequest
	if !s.decodeEnrollment(w, request, &input) {
		return
	}
	edPublic, edErr := decodeBase64(input.Ed25519PublicKeyBase64)
	noisePublic, noiseErr := decodeBase64(input.X25519PublicKeyBase64)
	capability, capabilityErr := decodeBase64(input.CapabilityHashBase64)
	if input.Token != "" || edErr != nil || noiseErr != nil || capabilityErr != nil || len(edPublic) != 32 || len(noisePublic) != 32 || len(capability) != 32 || len(input.DisplayName) > 256 {
		s.enrollmentError(w, store.EnrollmentError("invalid_request"))
		return
	}
	challenge := make([]byte, 32)
	if _, err := rand.Read(challenge); err != nil {
		s.enrollmentError(w, err)
		return
	}
	requestID := uuid.New()
	expiresAt := time.Now().Add(time.Minute).UnixMilli()
	s.enrollmentMu.Lock()
	s.expireEnrollmentsLocked(time.Now().UnixMilli())
	if len(s.enrollments) >= 10_000 {
		s.enrollmentMu.Unlock()
		s.metrics.EnrollmentRateLimited.Add(1)
		w.Header().Set("Retry-After", "30")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "rate_limited"})
		return
	}
	s.enrollments[requestID] = pendingEnrollment{input: store.EnrollmentInput{Role: 1, DisplayName: input.DisplayName, Ed25519PublicKey: edPublic, X25519PublicKey: noisePublic, CapabilityHash: capability}, challenge: challenge, expiresAt: expiresAt}
	s.enrollmentMu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"requestId": requestID.String(), "challenge": base64.StdEncoding.EncodeToString(challenge), "expiresAtMs": expiresAt})
}

func (s *Server) completeEnrollment(w http.ResponseWriter, request *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.enrollmentRate(w, request, "complete", 20) {
		return
	}
	var input struct {
		RequestID string `json:"requestId"`
		Signature string `json:"signature"`
		Token     string `json:"token,omitempty"`
	}
	if !s.decodeEnrollment(w, request, &input) {
		return
	}
	requestID, err := uuid.Parse(input.RequestID)
	if err != nil {
		s.enrollmentError(w, store.EnrollmentError("invalid_request"))
		return
	}
	s.enrollmentMu.Lock()
	pending, exists := s.enrollments[requestID]
	delete(s.enrollments, requestID)
	s.enrollmentMu.Unlock()
	if !exists || time.Now().UnixMilli() >= pending.expiresAt {
		s.enrollmentError(w, store.EnrollmentError("challenge_expired"))
		return
	}
	signature, err := decodeBase64(input.Signature)
	transcript := protocol.EnrollmentSignatureInput(s.cfg.PublicURL, requestID[:], pending.challenge, pending.expiresAt, pending.input.Ed25519PublicKey, pending.input.X25519PublicKey, pending.input.CapabilityHash, pending.input.DisplayName)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(pending.input.Ed25519PublicKey, transcript, signature) {
		s.enrollmentError(w, store.EnrollmentError("invalid_signature"))
		return
	}
	pending.input.Token = input.Token
	device, created, err := s.store.RegisterDaemon(request.Context(), pending.input, s.cfg.EnrollmentMode, s.cfg.MaxDaemons, true)
	if err != nil {
		s.enrollmentError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		s.metrics.EnrollmentCreated.Add(1)
	} else {
		s.metrics.EnrollmentRecovered.Add(1)
	}
	s.metrics.EnrollmentSuccess.Add(1)
	writeJSON(w, status, map[string]any{"deviceId": device.ID.String(), "created": created})
}

func (s *Server) enrollmentError(w http.ResponseWriter, err error) {
	code := "temporarily_unavailable"
	status := http.StatusServiceUnavailable
	var enrollmentErr store.EnrollmentError
	if errors.As(err, &enrollmentErr) {
		code = string(enrollmentErr)
		switch code {
		case "invalid_request":
			status = http.StatusBadRequest
		case "invalid_signature", "invalid_invite":
			status = http.StatusUnauthorized
		case "invite_required", "enrollment_disabled", "device_revoked":
			status = http.StatusForbidden
		case "identity_conflict", "challenge_expired":
			status = http.StatusConflict
		case "daemon_capacity_reached":
			status = http.StatusServiceUnavailable
		}
	}
	s.metrics.EnrollmentFailure.Add(1)
	s.metrics.RecordEnrollmentFailure(code)
	writeJSON(w, status, map[string]string{"code": code})
}

func (s *Server) expireEnrollmentsLocked(now int64) {
	for requestID, pending := range s.enrollments {
		if now >= pending.expiresAt {
			delete(s.enrollments, requestID)
		}
	}
}

func (s *Server) cleanEnrollments(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.enrollmentMu.Lock()
			s.expireEnrollmentsLocked(now.UnixMilli())
			s.enrollmentMu.Unlock()
		}
	}
}

func (s *Server) peerIP(request *http.Request) string {
	direct := peerIP(request)
	trusted := func(address string) bool {
		ip := net.ParseIP(address)
		for _, cidr := range s.cfg.TrustedProxyCIDRs {
			_, network, err := net.ParseCIDR(cidr)
			if err == nil && network.Contains(ip) {
				return true
			}
		}
		return false
	}
	if !trusted(direct) {
		return direct
	}
	forwarded := request.Header.Get("X-Forwarded-For")
	if forwarded == "" || len(forwarded) > 4096 {
		return direct
	}
	addresses := strings.Split(forwarded, ",")
	for _, address := range addresses {
		if net.ParseIP(strings.TrimSpace(address)) == nil {
			return direct
		}
	}
	for index := len(addresses) - 1; index >= 0; index-- {
		address := net.ParseIP(strings.TrimSpace(addresses[index])).String()
		if !trusted(address) {
			return address
		}
	}
	return direct
}
