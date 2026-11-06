package daemon

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hurf/work-anywhere/desktop/internal/ipc"
	"github.com/hurf/work-anywhere/desktop/internal/protocol"
	"github.com/hurf/work-anywhere/desktop/internal/remote"
	qrcode "github.com/skip2/go-qrcode"
)

//go:embed web/*
var webFiles embed.FS

type adminServer struct {
	manager  *Manager
	server   *http.Server
	listener net.Listener
	mu       sync.Mutex
	tokens   map[string]time.Time
	sessions map[string]adminSession
}

type adminSession struct {
	CSRF    string
	Expires time.Time
}

func startAdmin(manager *Manager) (*adminServer, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	a := &adminServer{manager: manager, listener: listener, tokens: make(map[string]time.Time), sessions: make(map[string]adminSession)}
	mux := http.NewServeMux()
	assets, _ := fs.Sub(webFiles, "web")
	mux.Handle("GET /", http.FileServer(http.FS(assets)))
	mux.HandleFunc("POST /auth/exchange", a.exchange)
	mux.HandleFunc("GET /api/status", a.status)
	mux.HandleFunc("GET /api/ws", a.ws)
	mux.HandleFunc("POST /api/pair-qr", a.pairQR)
	a.server = &http.Server{Handler: securityHeaders(mux), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	go func() { _ = a.server.Serve(listener) }()
	return a, nil
}

func (a *adminServer) pairQR(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorize(r); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	if err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	var request struct {
		URI string `json:"uri"`
	}
	if json.Unmarshal(body, &request) != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if _, secret, parseErr := remote.ParseQR(request.URI); parseErr != nil {
		http.Error(w, "invalid pairing URI", http.StatusBadRequest)
		return
	} else {
		for index := range secret {
			secret[index] = 0
		}
	}
	image, err := qrcode.Encode(request.URI, qrcode.Medium, 384)
	if err != nil {
		http.Error(w, "cannot render pairing QR", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(image)
}

func (a *adminServer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return a.server.Shutdown(ctx)
}

func (a *adminServer) OneTimeURL() string {
	token := randomToken(32)
	a.mu.Lock()
	a.tokens[token] = time.Now().Add(time.Minute)
	a.mu.Unlock()
	return fmt.Sprintf("http://%s/#token=%s", a.listener.Addr(), token)
}

func (a *adminServer) exchange(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(r) {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	var request struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(body, &request) != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	a.mu.Lock()
	expires, ok := a.tokens[request.Token]
	delete(a.tokens, request.Token)
	if !ok || time.Now().After(expires) {
		a.mu.Unlock()
		http.Error(w, "expired token", 401)
		return
	}
	sessionID, csrf := randomToken(32), randomToken(24)
	a.sessions[sessionID] = adminSession{CSRF: csrf, Expires: time.Now().Add(8 * time.Hour)}
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "wa_session", Value: sessionID, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: false, MaxAge: 8 * 3600})
	writeJSON(w, map[string]string{"csrf": csrf})
}

func (a *adminServer) status(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorize(r); !ok {
		http.Error(w, "unauthorized", 401)
		return
	}
	response := a.manager.control(protocol.ControlRequest{Type: "request", ID: "http", Method: "system.status", Params: json.RawMessage(`{}`)})
	// Agent diagnostics and startup settings contain local paths; they are
	// added only for the loopback administration page, not system.status.
	if result, ok := response.Result.(map[string]any); ok && response.Error == nil {
		result["agents"] = a.manager.agentSnapshots()
		result["startup"] = startupSnapshot()
	}
	writeJSON(w, response)
}

func (a *adminServer) ws(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authorize(r); !ok {
		http.Error(w, "unauthorized", 401)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{a.listener.Addr().String()}})
	if err != nil {
		return
	}
	conn.SetReadLimit(ipc.DefaultMaxFrame)
	defer conn.CloseNow()
	events, unsubscribe := a.manager.subscribe()
	defer unsubscribe()
	var writeMu sync.Mutex
	go func() {
		for event := range events {
			data, _ := json.Marshal(event)
			writeMu.Lock()
			err := conn.Write(r.Context(), websocket.MessageText, data)
			writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	for {
		kind, data, readErr := conn.Read(r.Context())
		if readErr != nil {
			return
		}
		if kind != websocket.MessageText {
			continue
		}
		var request protocol.ControlRequest
		if json.Unmarshal(data, &request) != nil {
			continue
		}
		response, _ := json.Marshal(a.manager.control(request))
		writeMu.Lock()
		writeErr := conn.Write(r.Context(), websocket.MessageText, response)
		writeMu.Unlock()
		if writeErr != nil {
			return
		}
	}
}

func (a *adminServer) authorize(r *http.Request) (adminSession, bool) {
	cookie, err := r.Cookie("wa_session")
	if err != nil {
		return adminSession{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	session, ok := a.sessions[cookie.Value]
	if !ok || time.Now().After(session.Expires) {
		delete(a.sessions, cookie.Value)
		return adminSession{}, false
	}
	provided := r.Header.Get("X-WA-CSRF")
	if provided == "" {
		provided = r.URL.Query().Get("csrf")
	}
	if provided != session.CSRF {
		return adminSession{}, false
	}
	return session, true
}

func (a *adminServer) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "http://"+a.listener.Addr().String()
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' blob:; connect-src 'self' ws:")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func randomToken(size int) string {
	data := make([]byte, size)
	_, _ = rand.Read(data)
	return base64.RawURLEncoding.EncodeToString(data)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
