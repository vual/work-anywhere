package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vual/work-anywhere/relay/internal/devmobile"
)

//go:embed web/*
var assets embed.FS

func main() {
	listen := flag.String("listen", "127.0.0.1:18081", "local development UI address")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client := devmobile.New()
	web, _ := fs.Sub(assets, "web")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(web)))
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, client.Status()) })
	mux.HandleFunc("POST /api/pair", func(w http.ResponseWriter, request *http.Request) {
		var input struct {
			URI string `json:"uri"`
		}
		if decode(request, &input) != nil || input.URI == "" {
			writeJSON(w, 400, map[string]string{"error": "pairing URI is required"})
			return
		}
		if err := client.Pair(ctx, input.URI); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, client.Status())
	})
	mux.HandleFunc("POST /api/call", func(w http.ResponseWriter, request *http.Request) {
		var input struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if decode(request, &input) != nil || input.Method == "" {
			writeJSON(w, 400, map[string]string{"error": "method is required"})
			return
		}
		callCtx, cancel := context.WithTimeout(request.Context(), 60*time.Second)
		defer cancel()
		result, err := client.Call(callCtx, input.Method, input.Params)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(result)
	})
	server := &http.Server{Addr: *listen, Handler: securityHeaders(mux), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	fmt.Printf("Work Anywhere development mobile: http://%s\n", *listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func decode(request *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, 2<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; style-src 'self'; script-src 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, request)
	})
}
