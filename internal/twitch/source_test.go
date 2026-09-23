package twitch_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/am-kenny/ampulsar/internal/domain"
	"github.com/am-kenny/ampulsar/internal/twitch"
)

const username = "test_user"

var resolvedChannel = domain.Channel{
	Platform:    domain.Twitch,
	ID:          "100",
	Username:    username,
	DisplayName: "Test User",
}

// newSource serves a valid token, then the given body for the Helix call.
func newSource(t *testing.T, body string) *twitch.Source {
	t.Helper()
	return twitch.NewSource(newClient(t, func(w http.ResponseWriter, _ *http.Request, isToken bool) {
		if isToken {
			_, _ = w.Write([]byte(okToken))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
}

func TestResolveChannelHappyPath(t *testing.T) {
	s := newSource(t, `{"data":[{"id":"100","login":"test_user","display_name":"Test User"}]}`)

	ch, err := s.ResolveChannel(context.Background(), username)
	if err != nil {
		t.Fatalf("ResolveChannel: %v", err)
	}
	if ch != resolvedChannel {
		t.Errorf("channel = %+v, want %+v", ch, resolvedChannel)
	}
}

func TestResolveChannelUnknownUser(t *testing.T) {
	s := newSource(t, emptyData)

	if ch, err := s.ResolveChannel(context.Background(), username); err == nil {
		t.Fatalf("got %+v, want an error", ch)
	}
}

func TestFetchStreamMapsSnapshot(t *testing.T) {
	s := newSource(t, `{"data":[{"id":"200","game_name":"Factorio","title":"Title with & and <tags>","started_at":"2026-09-23T18:00:00Z"}]}`)

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
		Game:      "Factorio",
		StartedAt: time.Date(2026, 9, 23, 18, 0, 0, 0, time.UTC),
		URL:       "https://www.twitch.tv/test_user",
	}
	if *snap != want {
		t.Errorf("snapshot = %+v, want %+v", *snap, want)
	}
}

func TestFetchStreamOfflineReturnsNil(t *testing.T) {
	s := newSource(t, emptyData)

	snap, err := s.FetchStream(context.Background(), resolvedChannel)
	if err != nil {
		t.Fatalf("FetchStream: %v", err)
	}
	if snap != nil {
		t.Errorf("want nil snapshot, got %+v", *snap)
	}
}

func TestFetchStreamBadStartTime(t *testing.T) {
	s := newSource(t, `{"data":[{"id":"200","started_at":"yesterday"}]}`)

	if snap, err := s.FetchStream(context.Background(), resolvedChannel); err == nil {
		t.Fatalf("got (%v, nil), want an error", snap)
	}
}

func TestFetchRecordingPicksMatchingStream(t *testing.T) {
	s := newSource(t, `{"data":[
		{"stream_id":"199","title":"Old","url":"https://www.twitch.tv/videos/1","duration":"1h"},
		{"stream_id":"200","title":"New","url":"https://www.twitch.tv/videos/2","duration":"3h8m33s"}
	]}`)

	rec, err := s.FetchRecording(context.Background(), resolvedChannel, "200")
	if err != nil {
		t.Fatalf("FetchRecording: %v", err)
	}
	if rec == nil {
		t.Fatal("want a recording, got nil")
	}

	want := domain.Recording{
		URL:      "https://www.twitch.tv/videos/2",
		Title:    "New",
		Duration: 3*time.Hour + 8*time.Minute + 33*time.Second,
	}
	if *rec != want {
		t.Errorf("recording = %+v, want %+v", *rec, want)
	}
}

func TestFetchRecordingNotYetAvailable(t *testing.T) {
	s := newSource(t, emptyData)

	rec, err := s.FetchRecording(context.Background(), resolvedChannel, "200")
	if rec != nil || err != nil {
		t.Fatalf("FetchRecording = %v, %v; want nil, nil", rec, err)
	}
}

func TestFetchRecordingBadDuration(t *testing.T) {
	s := newSource(t, `{"data":[{"stream_id":"200","duration":"forever"}]}`)

	if rec, err := s.FetchRecording(context.Background(), resolvedChannel, "200"); err == nil {
		t.Fatalf("got (%v, nil), want an error", rec)
	}
}
