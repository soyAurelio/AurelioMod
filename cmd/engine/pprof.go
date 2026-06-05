package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	_ "net/http/pprof" // side-effect: registers /debug/pprof/ handlers
	"strings"
	"time"

	"github.com/soyAurelio/AurelioMod/internal/paseto"
)

// newPprofTokenManager creates a PASETO TokenManager from a hex-encoded
// Ed25519 secret key (PPROF_ADMIN_KEY).
func newPprofTokenManager(keyHex string) (*paseto.TokenManager, error) {
	tm, err := paseto.NewFromHex(keyHex)
	if err != nil {
		return nil, fmt.Errorf("pprof paseto init: %w", err)
	}
	return tm, nil
}

// startPprofServer starts the admin pprof HTTP server on port :6060.
// It blocks, so it should be called in a goroutine.
func startPprofServer(tm *paseto.TokenManager) {
	slog.Info("pprof admin server starting", "port", 6060)
	srv := &http.Server{
		Addr:              ":6060",
		Handler:           pprofMux(tm),
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("pprof admin server failed", "error", err)
	}
}

// pprofMux returns an http.Handler that serves Go pprof endpoints
// (/debug/pprof/) behind PASETO v4 Bearer token authentication.
// The handler wraps http.DefaultServeMux, which is populated by the
// side-effect import of net/http/pprof.
func pprofMux(tm *paseto.TokenManager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			writeError(w, http.StatusUnauthorized, "missing admin token")
			return
		}

		token := strings.TrimPrefix(auth, "Bearer ")
		if token == auth {
			writeError(w, http.StatusUnauthorized, "missing admin token")
			return
		}

		_, err := tm.VerifyToken(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid admin token")
			return
		}

		http.DefaultServeMux.ServeHTTP(w, r)
	})
}

// writeError writes a JSON error response without trailing newline.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	data, _ := json.Marshal(map[string]string{"error": msg})
	_, _ = w.Write(data)
}
