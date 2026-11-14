// Package health serves the liveness probe over HTTP.
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

// Check describes one loop that must keep running for the process to count as alive
type Check struct {
	Name   string
	Last   func() time.Time // when the loop last finished a pass, zero before the first run
	MaxAge time.Duration    // how old Last may get before the check fails
}

type Server struct {
	listener net.Listener
	server   *http.Server
}

// Listen binds addr and returns a Server that answers the probe once Run is called.
func Listen(addr string, checks ...Check) (*Server, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %q: %w", addr, err)
	}

	return &Server{
		listener: listener,
		server: &http.Server{
			Handler:           NewHandler(checks, time.Now),
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
// /livez answers 200 while every check is fresh and 503 once one of them is stale.
func NewHandler(checks []Check, now func() time.Time) http.Handler {
	started := now()

	mux := http.NewServeMux()

	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		for _, check := range checks {
			last := check.Last()
			if last.IsZero() {
				last = started
			}

			if age := now().Sub(last); age > check.MaxAge {
				slog.Warn("health: check is stale", "check", check.Name, "age", age, "max_age", check.MaxAge)
				respond(w, http.StatusServiceUnavailable, "stale: "+check.Name)
				return
			}
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
