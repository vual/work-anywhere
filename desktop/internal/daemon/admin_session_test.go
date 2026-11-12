package daemon

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

type fakeListener struct{ addr fakeAddr }

func (l fakeListener) Accept() (conn net.Conn, err error) { return nil, net.ErrClosed }
func (l fakeListener) Close() error                       { return nil }
func (l fakeListener) Addr() net.Addr                     { return l.addr }

func testAdmin() *adminServer {
	return &adminServer{listener: fakeListener{addr: "127.0.0.1:1"}, tokens: make(map[string]time.Time), sessions: make(map[string]adminSession)}
}

func exchangeSession(t *testing.T, a *adminServer) (*http.Cookie, string) {
	t.Helper()
	token := "one-time"
	a.tokens[token] = time.Now().Add(time.Minute)
	request := httptest.NewRequest(http.MethodPost, "/auth/exchange", strings.NewReader(`{"token":"one-time"}`))
	request.Header.Set("Origin", "http://127.0.0.1:1")
	recorder := httptest.NewRecorder()
	a.exchange(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("exchange: %d %s", recorder.Code, recorder.Body.String())
	}
	var body struct{ CSRF string }
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.CSRF == "" {
		t.Fatalf("csrf: %v %s", err, recorder.Body.String())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "wa_session" {
		t.Fatalf("cookies: %+v", cookies)
	}
	if cookies[0].MaxAge != 0 || !cookies[0].Expires.IsZero() {
		t.Fatalf("session cookie must not carry a fixed lifetime: %+v", cookies[0])
	}
	return cookies[0], body.CSRF
}

func authorizedRequest(cookie *http.Cookie, csrf string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	request.AddCookie(cookie)
	request.Header.Set("X-WA-CSRF", csrf)
	return request
}

func TestAdminSessionSlidesWhileInUse(t *testing.T) {
	a := testAdmin()
	cookie, csrf := exchangeSession(t, a)
	// Simulate a page opened long ago: without sliding expiry the original
	// deadline would have passed.
	a.sessions[cookie.Value] = adminSession{CSRF: csrf, Expires: time.Now().Add(time.Minute)}
	if _, ok := a.authorize(authorizedRequest(cookie, csrf)); !ok {
		t.Fatal("active session rejected")
	}
	if remaining := time.Until(a.sessions[cookie.Value].Expires); remaining < adminSessionIdle-time.Minute {
		t.Fatalf("session not extended: %s left", remaining)
	}
}

func TestAdminSessionExpiresWhenIdleOrForged(t *testing.T) {
	a := testAdmin()
	cookie, csrf := exchangeSession(t, a)
	if _, ok := a.authorize(authorizedRequest(cookie, "wrong")); ok {
		t.Fatal("wrong CSRF accepted")
	}
	a.sessions[cookie.Value] = adminSession{CSRF: csrf, Expires: time.Now().Add(-time.Second)}
	if _, ok := a.authorize(authorizedRequest(cookie, csrf)); ok {
		t.Fatal("idle session accepted")
	}
	if _, exists := a.sessions[cookie.Value]; exists {
		t.Fatal("expired session not removed")
	}
	// The one-time token cannot be exchanged twice.
	request := httptest.NewRequest(http.MethodPost, "/auth/exchange", strings.NewReader(`{"token":"one-time"}`))
	request.Header.Set("Origin", "http://127.0.0.1:1")
	recorder := httptest.NewRecorder()
	a.exchange(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("reused token: %d", recorder.Code)
	}
}

func TestAdminExchangePrunesExpiredSessions(t *testing.T) {
	a := testAdmin()
	a.sessions["stale"] = adminSession{CSRF: "x", Expires: time.Now().Add(-time.Second)}
	exchangeSession(t, a)
	if _, exists := a.sessions["stale"]; exists || len(a.sessions) != 1 {
		t.Fatalf("sessions: %d", len(a.sessions))
	}
}
