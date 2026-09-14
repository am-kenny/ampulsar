package twitch_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/am-kenny/ampulsar/internal/twitch"
)

const (
	token     = "test_token"
	okToken   = `{"access_token":"` + token + `","expires_in":3600}`
	emptyData = `{"data":[]}`
)

// helix serves a fixed token response, then the given body for the Helix call.
// It mirrors telegram's newClient: one call in, assert the result out.
func helix(t *testing.T, body string) *twitch.Client {
	t.Helper()
	return newClient(t, func(w http.ResponseWriter, r *http.Request, isToken bool) {
		if isToken {
			_, _ = w.Write([]byte(okToken))
			return
		}
		_, _ = w.Write([]byte(body))
	})
}

// newClient backs both the auth and API URLs with one server, dispatching by
// path. The handler is told whether the request is the token request.
func newClient(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, isToken bool), opts ...twitch.Option) *twitch.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r, strings.HasPrefix(r.URL.Path, "/oauth2/token"))
	}))
	t.Cleanup(srv.Close)
	return twitch.NewClient("id", "secret",
		append([]twitch.Option{twitch.WithAuthURL(srv.URL), twitch.WithAPIURL(srv.URL)}, opts...)...,
	)
}

func TestFetchUserHappyPath(t *testing.T) {
	c := helix(t, `{"data":[{"id":"u1","login":"streamer","display_name":"Streamer"}]}`)
	u, err := c.FetchUserByUsername(context.Background(), "streamer")
	if err != nil {
		t.Fatalf("FetchUserByUsername: %v", err)
	}
	if u.ID != "u1" || u.DisplayName != "Streamer" {
		t.Errorf("user = %+v", u)
	}
}

func TestUserNotFound(t *testing.T) {
	c := helix(t, emptyData)
	if _, err := c.FetchUserByUsername(context.Background(), "ghost"); err == nil {
		t.Fatal("want error for missing user, got nil")
	}
}

func TestStreamOfflineReturnsNil(t *testing.T) {
	c := helix(t, emptyData)
	s, err := c.FetchStreamByUsername(context.Background(), "streamer")
	if err != nil {
		t.Fatalf("FetchStreamByUsername: %v", err)
	}
	if s != nil {
		t.Errorf("want nil stream, got %+v", s)
	}
}

func TestHelixAPIError(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request, isToken bool) {
		if isToken {
			_, _ = w.Write([]byte(okToken))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	})
	_, err := c.FetchStreamByUsername(context.Background(), "a")
	var apiErr *twitch.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 {
		t.Fatalf("want *APIError 500, got %v", err)
	}
}

func TestRequestCarriesAuthAndParams(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request, isToken bool) {
		if isToken {
			_, _ = w.Write([]byte(okToken))
			return
		}
		if got := r.URL.Query().Get("login"); got != "streamer" {
			t.Errorf("login param = %q, want streamer", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("authorization = %q, want Bearer "+token, got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"u1"}]}`))
	})
	if _, err := c.FetchUserByUsername(context.Background(), "streamer"); err != nil {
		t.Fatalf("FetchUserByUsername: %v", err)
	}
}

// The token-flow tests count endpoint hits, which the flat helper cannot do, so
// they use a counting handler

func TestTokenReusedAcrossCalls(t *testing.T) {
	var tokenHits atomic.Int32
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request, isToken bool) {
		if isToken {
			tokenHits.Add(1)
			_, _ = w.Write([]byte(okToken))
			return
		}
		_, _ = w.Write([]byte(emptyData))
	})

	_, _ = c.FetchStreamByUsername(context.Background(), "a")
	_, _ = c.FetchStreamByUsername(context.Background(), "b")

	if got := tokenHits.Load(); got != 1 {
		t.Errorf("token fetched %d times, want 1 (reused)", got)
	}
}

func TestUnauthorizedRefetchesAndRetries(t *testing.T) {
	var tokenHits, helixHits atomic.Int32
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request, isToken bool) {
		if isToken {
			tokenHits.Add(1)
			_, _ = w.Write([]byte(okToken))
			return
		}
		if helixHits.Add(1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"Unauthorized"}`))
			return
		}
		_, _ = w.Write([]byte(emptyData))
	})

	if _, err := c.FetchStreamByUsername(context.Background(), "a"); err != nil {
		t.Fatalf("want success after retry, got %v", err)
	}
	if got := tokenHits.Load(); got != 2 {
		t.Errorf("token fetched %d times, want 2 (initial + refetch on 401)", got)
	}
	if got := helixHits.Load(); got != 2 {
		t.Errorf("helix called %d times, want 2 (401 + retry)", got)
	}
}

func TestExpiredTokenRefetches(t *testing.T) {
	now := time.Unix(0, 0)
	var tokenHits atomic.Int32
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request, isToken bool) {
		if isToken {
			tokenHits.Add(1)
			_, _ = w.Write([]byte(`{"access_token":"` + token + `","expires_in":60}`))
			return
		}
		_, _ = w.Write([]byte(emptyData))
	}, twitch.WithClock(func() time.Time { return now }))

	_, _ = c.FetchStreamByUsername(context.Background(), "a")
	now = now.Add(10 * time.Minute) // past the 60s expiry
	_, _ = c.FetchStreamByUsername(context.Background(), "a")

	if got := tokenHits.Load(); got != 2 {
		t.Errorf("token fetched %d times, want 2 (refetch after expiry)", got)
	}
}
