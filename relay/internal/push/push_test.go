package push

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestAPNsUsesTokenAuthAndGenericPayload(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(request.Header.Get("authorization"), "bearer ") || request.Header.Get("apns-topic") != "com.example.app" {
			t.Fatal("missing APNs authentication headers")
		}
		body, _ := io.ReadAll(request.Body)
		if strings.Contains(string(body), "conversation") || !strings.Contains(string(body), "notificationId") {
			t.Fatalf("unexpected APNs payload: %s", body)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	provider, err := NewAPNs(APNsConfig{TeamID: "TEAM", KeyID: "KEY", PrivateKeyPEM: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), Client: client})
	if err != nil {
		t.Fatal(err)
	}
	if err = provider.Send(context.Background(), Endpoint{AppID: "com.example.app", Environment: "sandbox", Token: "device-token"}, Notification{ID: "notice", Hint: "action_required"}); err != nil {
		t.Fatal(err)
	}
}

func TestFCMInvalidTokenIsPermanent(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/projects/test-project/messages:send" {
			t.Fatalf("unexpected FCM path: %s", request.URL.Path)
		}
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(`{"error":{"status":"UNREGISTERED"}}`)), Header: make(http.Header)}, nil
	})}
	credentials, _ := json.Marshal(map[string]string{"project_id": "test-project"})
	provider, err := NewFCM(credentials, client)
	if err != nil {
		t.Fatal(err)
	}
	err = provider.Send(context.Background(), Endpoint{Token: "registration-token"}, Notification{ID: "notice", Hint: "generic"})
	if !IsInvalidToken(err) {
		t.Fatalf("expected invalid-token error, got %v", err)
	}
}
