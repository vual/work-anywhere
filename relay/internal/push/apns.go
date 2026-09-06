package push

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type APNsConfig struct {
	TeamID, KeyID string
	PrivateKeyPEM []byte
	Client        *http.Client
}

type APNs struct {
	teamID, keyID string
	key           *ecdsa.PrivateKey
	client        *http.Client
	mu            sync.Mutex
	token         string
	tokenAt       time.Time
}

func NewAPNs(cfg APNsConfig) (*APNs, error) {
	block, _ := pem.Decode(cfg.PrivateKeyPEM)
	if cfg.TeamID == "" || cfg.KeyID == "" || block == nil {
		return nil, errors.New("invalid APNs configuration")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse APNs key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("APNs key is not ECDSA")
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &APNs{teamID: cfg.TeamID, keyID: cfg.KeyID, key: key, client: client}, nil
}

func (a *APNs) Send(ctx context.Context, endpoint Endpoint, notification Notification) error {
	token, err := a.authorizationToken()
	if err != nil {
		return err
	}
	text := notificationText(notification.Hint)
	body, _ := json.Marshal(map[string]any{"aps": map[string]any{"alert": text, "sound": "default", "thread-id": "work-anywhere"}, "notificationId": notification.ID})
	host := "https://api.push.apple.com"
	if endpoint.Environment == "sandbox" {
		host = "https://api.sandbox.push.apple.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, host+"/3/device/"+endpoint.Token, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "bearer "+token)
	req.Header.Set("apns-topic", endpoint.AppID)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	if notification.CollapseKey != "" {
		req.Header.Set("apns-collapse-id", notification.CollapseKey)
	}
	response, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	var failure struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&failure)
	if response.StatusCode == http.StatusGone || failure.Reason == "BadDeviceToken" || failure.Reason == "Unregistered" || failure.Reason == "DeviceTokenNotForTopic" {
		return &PermanentError{InvalidToken: true, Reason: "APNs rejected device token"}
	}
	if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests {
		return &PermanentError{Reason: "APNs rejected notification: " + failure.Reason}
	}
	return fmt.Errorf("APNs temporary response %d", response.StatusCode)
}

func (a *APNs) authorizationToken() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && time.Since(a.tokenAt) < 50*time.Minute {
		return a.token, nil
	}
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": a.keyID})
	claims, _ := json.Marshal(map[string]any{"iss": a.teamID, "iat": time.Now().Unix()})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, a.key, digest[:])
	if err != nil {
		return "", err
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	a.token = unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
	a.tokenAt = time.Now()
	return a.token, nil
}

func notificationText(hint string) string {
	switch hint {
	case "action_required":
		return "Work Anywhere 需要你的操作"
	case "turn_completed":
		return "桌面任务已完成"
	default:
		return "Work Anywhere 有新的状态"
	}
}
