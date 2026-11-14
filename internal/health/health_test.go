package health_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/am-kenny/ampulsar/internal/health"
)

var testStart = time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC)

func TestLivez(t *testing.T) {
	const maxAge = 5 * time.Minute

	tests := []struct {
		name       string
		elapsed    time.Duration // time since the handler was created
		poller     time.Duration // poller's last pass, as an offset from the start; negative means never
		reconciler time.Duration
		wantStatus int
		wantBody   string
	}{
		{"just started, no pass yet", 0, -1, -1, http.StatusOK, "ok"},
		{"no pass yet within max age", maxAge, -1, -1, http.StatusOK, "ok"},
		{"no pass yet past max age", maxAge + time.Second, -1, -1, http.StatusServiceUnavailable, "stale: poller"},
		{"both fresh", time.Hour, 58 * time.Minute, 59 * time.Minute, http.StatusOK, "ok"},
		{"exactly max age", time.Hour, 55 * time.Minute, 55 * time.Minute, http.StatusOK, "ok"},
		{"poller stale", time.Hour, 50 * time.Minute, 59 * time.Minute, http.StatusServiceUnavailable, "stale: poller"},
		{"reconciler stale", time.Hour, 59 * time.Minute, 50 * time.Minute, http.StatusServiceUnavailable, "stale: reconciler"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := testStart
			checks := []health.Check{
				{Name: "poller", Last: lastAt(tt.poller), MaxAge: maxAge},
				{Name: "reconciler", Last: lastAt(tt.reconciler), MaxAge: maxAge},
			}

			handler := health.NewHandler(checks, func() time.Time { return now })
			now = testStart.Add(tt.elapsed)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/livez", nil))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tt.wantBody {
				t.Errorf("body = %q, want %q", got, tt.wantBody)
			}
		})
	}
}

func TestRouting(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{"livez without checks", http.MethodGet, "/livez", http.StatusOK},
		{"livez rejects POST", http.MethodPost, "/livez", http.StatusMethodNotAllowed},
		{"unknown path", http.MethodGet, "/", http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := health.NewHandler(nil, time.Now)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestServerRun(t *testing.T) {
	srv, err := health.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Run(ctx)
	}()

	if got := get(t, "http://"+srv.Addr()+"/livez"); got != http.StatusOK {
		t.Errorf("livez status = %d, want %d", got, http.StatusOK)
	}

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after ctx was cancelled")
	}
}

func TestListenFailsOnBusyAddr(t *testing.T) {
	srv, err := health.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	if _, err = health.Listen(srv.Addr()); err == nil {
		t.Fatal("Listen on a busy address succeeded, want an error")
	}
}

// lastAt returns a Check.Last that reports a pass at testStart plus offset, or no pass for a negative offset
func lastAt(offset time.Duration) func() time.Time {
	return func() time.Time {
		if offset < 0 {
			return time.Time{}
		}
		return testStart.Add(offset)
	}
}

// get returns the status code of a GET request to url
func get(t *testing.T, url string) int {
	t.Helper()

	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()

	if _, err = io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("read body of %s: %v", url, err)
	}

	return resp.StatusCode
}
