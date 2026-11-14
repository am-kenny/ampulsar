package health

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

const (
	readHeaderTimeout = 5 * time.Second
	shutdownTimeout   = 5 * time.Second
)

type Server struct {
	listener net.Listener
	server   *http.Server
}

// Listen binds addr and returns a Server that answers the probes once Run is called.
// ready is asked on every readiness probe.
func Listen(addr string, ready func() bool) (*Server, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %q: %w", addr, err)
	}

	return &Server{
		listener: listener,
		server: &http.Server{
			Handler:           NewHandler(ready),
			ReadHeaderTimeout: readHeaderTimeout,
		},
	}, nil
}

// Addr returns the address the server is bound to
func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

// Run serves until ctx is done, then shuts the server down
func (s *Server) Run(ctx context.Context) {
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()

		if err := s.server.Shutdown(shutdownCtx); err != nil {
			slog.Warn("health: shutdown failed", "err", err)
		}
	}()

	if err := s.server.Serve(s.listener); !errors.Is(err, http.ErrServerClosed) {
		slog.Error("health: serve failed", "err", err)
		return
	}

	<-stopped
}

// NewHandler returns the probe routes
// /livez answers 200 as long as the process serves HTTP,
// /readyz answers 200 once ready returns true and 503 before.
func NewHandler(ready func() bool) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		respond(w, http.StatusOK, "ok")
	})

	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready() {
			respond(w, http.StatusServiceUnavailable, "not ready")
			return
		}
		respond(w, http.StatusOK, "ok")
	})

	return mux
}

func respond(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = fmt.Fprintln(w, body)
}
