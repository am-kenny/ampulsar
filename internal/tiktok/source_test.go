package tiktok_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/am-kenny/ampulsar/internal/domain"
	"github.com/am-kenny/ampulsar/internal/tiktok"
)

var resolvedChannel = domain.Channel{
	Platform:    domain.TikTok,
	ID:          "100",
	Username:    username,
	DisplayName: "Test User",
}

// newSource serves one fixed body for every request, for code that calls a
// single endpoint.
func newSource(t *testing.T, status int, body string) *tiktok.Source {
	t.Helper()
	return tiktok.NewSource(respond(t, status, body))
}

type route struct {
	status int
	body   string
}

// newSourceWithRoutes answers each path with its own response, for code that
// calls more than one endpoint.
func newSourceWithRoutes(t *testing.T, routes map[string]route) *tiktok.Source {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt, ok := routes[r.URL.Path]
		if !ok {
			t.Errorf("unexpected request to %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.WriteHeader(rt.status)
		_, _ = w.Write([]byte(rt.body))
	}))
	t.Cleanup(srv.Close)

	return tiktok.NewSource(tiktok.NewClient(tiktok.WithAPIURL(srv.URL)))
}

const (
	oembedPath   = "/oembed"
	userRoomPath = "/api-live/user/room/"
)

func TestResolveChannelHappyPath(t *testing.T) {
	s := newSourceWithRoutes(t, map[string]route{
		oembedPath:   {http.StatusOK, profile},
		userRoomPath: {http.StatusOK, liveRoom},
	})

	ch, err := s.ResolveChannel(context.Background(), username)
	if err != nil {
		t.Fatalf("ResolveChannel: %v", err)
	}
	if ch != resolvedChannel {
		t.Errorf("channel = %+v, want %+v", ch, resolvedChannel)
	}
}

// A user who has never gone live has no numeric id yet, which must not stop
// the channel from resolving.
func TestResolveChannelWithoutLiveHistory(t *testing.T) {
	s := newSourceWithRoutes(t, map[string]route{
		oembedPath:   {http.StatusOK, profile},
		userRoomPath: {http.StatusOK, userNotFound},
	})

	ch, err := s.ResolveChannel(context.Background(), username)
	if err != nil {
		t.Fatalf("ResolveChannel: %v", err)
	}

	want := domain.Channel{Platform: domain.TikTok, Username: username, DisplayName: "Test User"}
	if ch != want {
		t.Errorf("channel = %+v, want %+v", ch, want)
	}
}

func TestResolveChannelUnknownUser(t *testing.T) {
	s := newSourceWithRoutes(t, map[string]route{
		oembedPath: {http.StatusBadRequest, profileNotFound},
	})

	if ch, err := s.ResolveChannel(context.Background(), username); err == nil {
		t.Fatalf("got %+v, want an error", ch)
	}
}

func TestResolveChannelBlockedRoomLookup(t *testing.T) {
	s := newSourceWithRoutes(t, map[string]route{
		oembedPath:   {http.StatusOK, profile},
		userRoomPath: {http.StatusOK, captchaPage},
	})

	if ch, err := s.ResolveChannel(context.Background(), username); err == nil {
		t.Fatalf("got %+v, want an error", ch)
	}
}

func TestFetchStreamMapsSnapshot(t *testing.T) {
	s := newSource(t, http.StatusOK, liveRoom)

	snap, err := s.FetchStream(context.Background(), resolvedChannel)
	if err != nil {
		t.Fatalf("FetchStream: %v", err)
	}
	if snap == nil {
		t.Fatal("want a snapshot, got nil (offline)")
	}

	want := domain.Snapshot{
		StreamID:  "200",
		Title:     "Title with & and <tags>",
		StartedAt: time.Unix(startTime, 0).UTC(),
		URL:       "https://www.tiktok.com/@test_user/live",
	}
	if *snap != want {
		t.Errorf("snapshot = %+v, want %+v", *snap, want)
	}
}

// A missing start time must stay zero rather than becoming 1970.
func TestFetchStreamLeavesUnknownFieldsZero(t *testing.T) {
	const liveNoStart = `{"statusCode":0,"data":{"user":{"id":"100","roomId":"200","status":2},"liveRoom":{"title":"t","status":2}}}`

	s := newSource(t, http.StatusOK, liveNoStart)

	snap, err := s.FetchStream(context.Background(), resolvedChannel)
	if err != nil {
		t.Fatalf("FetchStream: %v", err)
	}
	if snap.Game != "" {
		t.Errorf("game = %q, want empty", snap.Game)
	}
	if !snap.StartedAt.IsZero() {
		t.Errorf("started at = %v, want zero", snap.StartedAt)
	}
}

func TestFetchStreamOfflineReturnsNil(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"stream ended", endedRoom},
		// The channel was confirmed to exist when it was resolved, so no LIVE room means the user has never gone live.
		{"never gone live", userNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSource(t, http.StatusOK, tt.body)

			snap, err := s.FetchStream(context.Background(), resolvedChannel)
			if err != nil {
				t.Fatalf("FetchStream: %v", err)
			}
			if snap != nil {
				t.Errorf("want nil snapshot, got %+v", *snap)
			}
		})
	}
}

func TestFetchStreamBlocked(t *testing.T) {
	s := newSource(t, http.StatusOK, captchaPage)

	if snap, err := s.FetchStream(context.Background(), resolvedChannel); err == nil {
		t.Fatalf("got (%v, nil), want an error", snap)
	}
}

func TestFetchRecordingReturnsNil(t *testing.T) {
	s := tiktok.NewSource(tiktok.NewClient())

	rec, err := s.FetchRecording(context.Background(), resolvedChannel, "200")
	if rec != nil || err != nil {
		t.Fatalf("FetchRecording = %v, %v; want nil, nil", rec, err)
	}
}

func TestPlatform(t *testing.T) {
	if got := tiktok.NewSource(tiktok.NewClient()).Platform(); got != "tiktok" {
		t.Errorf("platform = %q, want tiktok", got)
	}
}
