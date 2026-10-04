package remote

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
)

type remoteError struct {
	code       string
	retryAfter time.Duration
}

func (err *remoteError) Error() string { return err.code }

func ErrorCode(err error) string {
	var remoteErr *remoteError
	if errors.As(err, &remoteErr) {
		return remoteErr.code
	}
	return "temporarily_unavailable"
}

func enrollmentSignatureInput(origin string, requestID, challenge []byte, expiresAtMS int64, edPublic, noisePublic, capability []byte, displayName string) []byte {
	result := []byte("waw-relay-daemon-enrollment-v1\x00")
	for _, value := range [][]byte{[]byte(origin), requestID, challenge} {
		result = appendField(result, value)
	}
	result = binary.BigEndian.AppendUint64(result, uint64(expiresAtMS))
	for _, value := range [][]byte{edPublic, noisePublic, capability, []byte(displayName)} {
		result = appendField(result, value)
	}
	return result
}

func (s *Service) enrollmentHTTP(ctx context.Context, origin, path, method string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, origin+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(request *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed {
		return &remoteError{code: "relay_upgrade_required"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		failure := struct {
			Code string `json:"code"`
		}{}
		_ = json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&failure)
		code := failure.Code
		switch code {
		case "invalid_request", "invalid_signature", "challenge_expired", "invite_required", "invalid_invite", "enrollment_disabled", "identity_conflict", "device_revoked", "daemon_capacity_reached", "rate_limited", "temporarily_unavailable":
		default:
			code = "temporarily_unavailable"
			if response.StatusCode < 500 && response.StatusCode != 429 {
				code = "invalid_request"
			}
		}
		if response.StatusCode == 429 {
			code = "rate_limited"
		}
		retryAfter := time.Duration(0)
		if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 && seconds <= 86400 {
			retryAfter = time.Duration(seconds) * time.Second
		} else if deadline, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil {
			retryAfter = time.Until(deadline)
		}
		return &remoteError{code: code, retryAfter: retryAfter}
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(output); err != nil {
		return &remoteError{code: "invalid_response"}
	}
	return nil
}

func (s *Service) Register(ctx context.Context, displayName, token string) error {
	return s.register(ctx, displayName, token, true, s.snapshotState().Generation)
}

func (s *Service) register(ctx context.Context, displayName, token string, wakeOnFailure bool, generation uint64) error {
	s.enrollMu.Lock()
	defer s.enrollMu.Unlock()
	s.mu.Lock()
	if s.generation != generation || ctx.Err() != nil {
		s.mu.Unlock()
		return context.Canceled
	}
	if !s.state.Enabled || s.state.RelayURL == "" {
		s.mu.Unlock()
		return &remoteError{code: "remote_disabled"}
	}
	if time.Until(s.nextRetryAt) > 0 {
		delay := time.Until(s.nextRetryAt)
		s.mu.Unlock()
		return &remoteError{code: "rate_limited", retryAfter: delay}
	}
	if s.state.DeviceID != "" {
		s.mu.Unlock()
		return nil
	}
	if err := s.ensureIdentityLocked(); err != nil {
		s.recordErrorLocked(err)
		s.phase = "needs_attention"
		s.blocked = !retryable(err)
		s.mu.Unlock()
		return err
	}
	state := s.state
	s.phase = "registering"
	s.nextRetryAt = time.Time{}
	operationCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	s.registrationCancel = cancel
	s.mu.Unlock()
	defer cancel()
	err := s.registerOnce(operationCtx, state, displayName, token)
	s.mu.Lock()
	if s.generation == state.Generation && s.state.Enabled {
		s.recordErrorLocked(err)
		if err == nil {
			s.phase = "connecting"
			s.blocked = false
		} else {
			s.blocked = !retryable(err)
			if retryable(err) {
				s.nextRetryAt = time.Now().Add(retryDelay(err, time.Second))
			}
			if ErrorCode(err) == "invite_required" {
				s.phase = "invite_required"
			} else {
				s.phase = "needs_attention"
			}
		}
	}
	s.mu.Unlock()
	if err == nil || wakeOnFailure {
		s.signalWake()
	}
	return err
}

func (s *Service) registerOnce(ctx context.Context, state persistedState, displayName, token string) error {
	if displayName == "" {
		displayName = state.DisplayName
	}
	var policy struct {
		Protocol    string `json:"protocol"`
		Mode        string `json:"mode"`
		RelayOrigin string `json:"relayOrigin"`
	}
	if err := s.enrollmentHTTP(ctx, state.RelayURL, "/v1/enrollment/policy", http.MethodGet, nil, &policy); err != nil {
		return err
	}
	origin, err := normalizeRelayURL(policy.RelayOrigin)
	if err != nil || origin != state.RelayURL || policy.Protocol != "daemon-enrollment-v1" {
		return &remoteError{code: "identity_conflict"}
	}
	switch policy.Mode {
	case "invite", "open", "self_service", "disabled":
	default:
		return &remoteError{code: "relay_upgrade_required"}
	}
	s.mu.Lock()
	if s.generation != state.Generation || !s.state.Enabled {
		s.mu.Unlock()
		return context.Canceled
	}
	s.registrationMode = policy.Mode
	s.mu.Unlock()
	var challenge struct {
		RequestID   string `json:"requestId"`
		Challenge   string `json:"challenge"`
		ExpiresAtMS int64  `json:"expiresAtMs"`
	}
	input := map[string]string{"displayName": displayName, "ed25519PublicKey": base64.StdEncoding.EncodeToString(state.Ed25519Public), "x25519PublicKey": base64.StdEncoding.EncodeToString(state.X25519Public), "capabilityHash": base64.StdEncoding.EncodeToString(state.CapabilityHash)}
	if err = s.enrollmentHTTP(ctx, state.RelayURL, "/v1/enrollment/challenges", http.MethodPost, input, &challenge); err != nil {
		return err
	}
	requestID, err := uuid.Parse(challenge.RequestID)
	random, decodeErr := base64.StdEncoding.DecodeString(challenge.Challenge)
	if err != nil || decodeErr != nil || len(random) != 32 || challenge.ExpiresAtMS <= 0 {
		return &remoteError{code: "invalid_response"}
	}
	signature := ed25519.Sign(ed25519.PrivateKey(state.Ed25519Private), enrollmentSignatureInput(origin, requestID[:], random, challenge.ExpiresAtMS, state.Ed25519Public, state.X25519Public, state.CapabilityHash, displayName))
	var result struct {
		DeviceID string `json:"deviceId"`
		Created  bool   `json:"created"`
	}
	if err = s.enrollmentHTTP(ctx, state.RelayURL, "/v1/enrollment/complete", http.MethodPost, map[string]string{"requestId": requestID.String(), "signature": base64.StdEncoding.EncodeToString(signature), "token": token}, &result); err != nil {
		return err
	}
	if uuid.Validate(result.DeviceID) != nil {
		return &remoteError{code: "invalid_response"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != state.Generation || !s.state.Enabled || ctx.Err() != nil {
		return context.Canceled
	}
	next := s.state
	next.DeviceID = result.DeviceID
	next.DisplayName = displayName
	if err = s.store.PutSetting(remoteStateKey, next); err != nil {
		return err
	}
	s.state = next
	s.signalWake()
	return nil
}

func retryable(err error) bool {
	if err == nil {
		return true
	}
	switch ErrorCode(err) {
	case "temporarily_unavailable", "rate_limited", "challenge_expired":
		return true
	}
	return false
}

func (s *Service) recordErrorLocked(err error) {
	s.lastError = errString(err)
	s.lastErrorCode = ""
	if err != nil {
		s.lastErrorCode = ErrorCode(err)
	}
}

func retryDelay(err error, backoff time.Duration) time.Duration {
	var remoteErr *remoteError
	if errors.As(err, &remoteErr) && remoteErr.retryAfter > 0 {
		return remoteErr.retryAfter
	}
	var random [1]byte
	_, _ = rand.Read(random[:])
	return backoff/2 + time.Duration(random[0])*backoff/510
}
