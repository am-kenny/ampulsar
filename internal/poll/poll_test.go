package poll_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/am-kenny/ampulsar/internal/domain"
	"github.com/am-kenny/ampulsar/internal/poll"
	"github.com/am-kenny/ampulsar/internal/store"
)

type fakeSource struct {
	calls        []string
	snapshot     *domain.Snapshot
	recording    *domain.Recording
	recordingErr error
}

func (f *fakeSource) record(method string) {
	f.calls = append(f.calls, method)
}

func (f *fakeSource) FetchStream(_ context.Context, _ domain.Channel) (*domain.Snapshot, error) {
	f.record("FetchStream")
	return f.snapshot, nil
}

func (f *fakeSource) FetchRecording(_ context.Context, _ domain.Channel, _ string) (*domain.Recording, error) {
	f.record("FetchRecording")
	return f.recording, f.recordingErr
}

// newStore returns an in-memory store populated with session and deliveries
func newStore(t *testing.T, session *domain.Session, deliveries ...domain.Delivery) *store.Store {
	t.Helper()

	st := &store.Store{}

	if session != nil {
		if err := st.SetSession(*session); err != nil {
			t.Fatalf("SetSession: %v", err)
		}
	}

	for _, d := range deliveries {
		if err := st.SaveDelivery(d); err != nil {
			t.Fatalf("SaveDelivery: %v", err)
		}
	}

	return st
}

// mustGetSession returns the stored session and fails the test if there is none
func mustGetSession(t *testing.T, st *store.Store) *domain.Session {
	t.Helper()

	s := st.GetSession()
	if s == nil {
		t.Fatal("no session stored")
	}
	return s
}

var testNow = time.Date(2026, 9, 17, 20, 0, 0, 0, time.UTC)

func newPoller(src poll.Source, st poll.Store, onEnd domain.EndPolicy, opts ...poll.Option) *poll.Poller {
	opts = append(opts, poll.WithClock(func() time.Time { return testNow }))

	return poll.NewPoller(src, st,
		domain.Channel{Platform: domain.Twitch, ID: "u1", Username: "streamer"},
		poll.Config{Interval: time.Minute, EndGrace: 10 * time.Minute, WaitForRecording: onEnd.NeedsRecording()},
		opts...,
	)
}

func liveSnapshot() *domain.Snapshot {
	return &domain.Snapshot{StreamID: "s1", Title: "T", Game: "G"}
}

func liveSession() *domain.Session {
	return &domain.Session{
		Platform: domain.Twitch, ID: "u1",
		Username: "streamer",
		StreamID: "s1",
		State:    domain.SessionLive,
		Version:  1,
		Title:    "T",
		Game:     "G",
		URL:      "https://www.twitch.tv/streamer",
	}
}

func endedSession(ago time.Duration) *domain.Session {
	s := liveSession()
	s.State = domain.SessionEnded
	s.EndedAt = testNow.Add(-ago)
	return s
}

// finishedSession returns endedSession(ago) in the given state
func finishedSession(state domain.SessionState, ago time.Duration) *domain.Session {
	s := endedSession(ago)
	s.State = state
	return s
}

func liveDeliveryAt(version int) domain.Delivery {
	return domain.Delivery{
		StreamID:      "s1",
		Kind:          domain.DeliveryLive,
		State:         domain.DeliveryPublished,
		Ref:           domain.MessageRef{ID: "100", ChatID: "chat"},
		SyncedVersion: version,
	}
}

func doneDeliveryAt(version int) domain.Delivery {
	d := liveDeliveryAt(version)
	d.State = domain.DeliveryDone
	return d
}

func TestPoll(t *testing.T) {
	tests := []struct {
		name       string
		snapshot   *domain.Snapshot
		recording  *domain.Recording
		starting   *domain.Session
		deliveries []domain.Delivery
		onEnd      domain.EndPolicy

		wantSourceCalls []string
		wantStored      bool
		wantState       domain.SessionState // checked when the session is stored
		recordingErr    error
	}{
		{
			name:            "stays live",
			snapshot:        liveSnapshot(),
			starting:        liveSession(),
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream"},
			wantStored:      true,
			wantState:       domain.SessionLive,
		},
		{
			name:            "stays offline",
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream"},
		},
		{
			name:            "went live",
			snapshot:        liveSnapshot(),
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream"},
			wantStored:      true,
			wantState:       domain.SessionLive,
		},
		{
			name:            "offline edit in place archives with recording",
			starting:        liveSession(),
			recording:       &domain.Recording{URL: "u"},
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantStored:      true,
			wantState:       domain.SessionArchived,
		},
		{
			name:            "offline new message archives with recording",
			starting:        liveSession(),
			recording:       &domain.Recording{URL: "u"},
			onEnd:           domain.EndPolicyNewMessage,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantStored:      true,
			wantState:       domain.SessionArchived,
		},
		{
			name:            "offline delete closes without waiting",
			starting:        liveSession(),
			onEnd:           domain.EndPolicyDelete,
			wantSourceCalls: []string{"FetchStream"},
			wantStored:      true,
			wantState:       domain.SessionClosed,
		},
		{
			name:            "offline none closes without waiting",
			starting:        liveSession(),
			onEnd:           domain.EndPolicyNone,
			wantSourceCalls: []string{"FetchStream"},
			wantStored:      true,
			wantState:       domain.SessionClosed,
		},
		{
			name:            "offline replace archives with recording",
			starting:        liveSession(),
			recording:       &domain.Recording{URL: "u"},
			onEnd:           domain.EndPolicyReplace,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantStored:      true,
			wantState:       domain.SessionArchived,
		},
		{
			name:            "offline replace waits for recording",
			starting:        liveSession(),
			onEnd:           domain.EndPolicyReplace,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantStored:      true,
			wantState:       domain.SessionEnded,
		},
		{
			name:            "offline no recording keeps session",
			starting:        liveSession(),
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantStored:      true,
			wantState:       domain.SessionEnded,
		},
		{
			name:            "offline source without recordings closes immediately",
			starting:        liveSession(),
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantStored:      true,
			wantState:       domain.SessionClosed,
			recordingErr:    domain.ErrNoRecordings,
		},
		{
			name:            "ended past grace without recording closes",
			starting:        endedSession(11 * time.Minute),
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantStored:      true,
			wantState:       domain.SessionClosed,
		},
		{
			name:            "ended within grace with fetch error keeps session",
			starting:        endedSession(5 * time.Minute),
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantStored:      true,
			wantState:       domain.SessionEnded,
			recordingErr:    errors.New("unknown"),
		},
		{
			name:            "ended past grace with fetch error closes",
			starting:        endedSession(11 * time.Minute),
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantStored:      true,
			wantState:       domain.SessionClosed,
			recordingErr:    errors.New("unknown"),
		},
		{
			name:            "stream restarted archives old session",
			snapshot:        &domain.Snapshot{StreamID: "s2", Title: "T", Game: "G"},
			starting:        liveSession(),
			recording:       &domain.Recording{URL: "u"},
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantStored:      true,
			wantState:       domain.SessionArchived,
		},
		{
			name:            "archived with every delivery done is deleted",
			starting:        finishedSession(domain.SessionArchived, time.Minute),
			deliveries:      []domain.Delivery{doneDeliveryAt(1)},
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream"},
		},
		{
			name:            "closed without deliveries is deleted",
			starting:        finishedSession(domain.SessionClosed, time.Minute),
			deliveries:      []domain.Delivery{}, // not nil, so no live delivery is added
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream"},
		},
		{
			name:            "archived with pending delivery within grace is kept",
			starting:        finishedSession(domain.SessionArchived, time.Minute),
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream"},
			wantStored:      true,
			wantState:       domain.SessionArchived,
		},
		{
			name:            "closed with pending delivery past grace is deleted",
			starting:        finishedSession(domain.SessionClosed, 11*time.Minute),
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream"},
		},
		{
			name:            "new stream waits until the finished session is deleted",
			snapshot:        &domain.Snapshot{StreamID: "s2", Title: "T", Game: "G"},
			starting:        finishedSession(domain.SessionArchived, time.Minute),
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream"},
			wantStored:      true,
			wantState:       domain.SessionArchived,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &fakeSource{snapshot: tt.snapshot, recording: tt.recording, recordingErr: tt.recordingErr}
			deliveries := tt.deliveries
			if tt.starting != nil && deliveries == nil {
				// a stored session always comes with its live delivery
				deliveries = []domain.Delivery{liveDeliveryAt(1)}
			}
			st := newStore(t, tt.starting, deliveries...)

			newPoller(src, st, tt.onEnd).Poll(context.Background())

			if !slices.Equal(src.calls, tt.wantSourceCalls) {
				t.Errorf("source calls = %v, want %v", src.calls, tt.wantSourceCalls)
			}
			if got := st.GetSession() != nil; got != tt.wantStored {
				t.Fatalf("session stored = %v, want %v", got, tt.wantStored)
			}
			if !tt.wantStored {
				return
			}

			if got := mustGetSession(t, st).State; got != tt.wantState {
				t.Errorf("state = %q, want %q", got, tt.wantState)
			}
		})
	}
}

func TestPollStreamInfoChange(t *testing.T) {
	tests := []struct {
		name     string
		snapshot *domain.Snapshot
		version  int // starting session version

		wantTitle   string
		wantGame    string
		wantVersion int
	}{
		{
			name:        "unchanged does nothing",
			snapshot:    liveSnapshot(),
			version:     1,
			wantTitle:   "T",
			wantGame:    "G",
			wantVersion: 1,
		},
		{
			name:        "title change updates session",
			snapshot:    &domain.Snapshot{StreamID: "s1", Title: "T2", Game: "G"},
			version:     1,
			wantTitle:   "T2",
			wantGame:    "G",
			wantVersion: 2,
		},
		{
			name:        "game change updates session",
			snapshot:    &domain.Snapshot{StreamID: "s1", Title: "T", Game: "G2"},
			version:     1,
			wantTitle:   "T",
			wantGame:    "G2",
			wantVersion: 2,
		},
		{
			name:        "title and game change bump version once",
			snapshot:    &domain.Snapshot{StreamID: "s1", Title: "T2", Game: "G2"},
			version:     1,
			wantTitle:   "T2",
			wantGame:    "G2",
			wantVersion: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &fakeSource{snapshot: tt.snapshot}
			session := liveSession()
			session.Version = tt.version
			st := newStore(t, session, liveDeliveryAt(1))

			newPoller(src, st, domain.EndPolicyEditInPlace).Poll(context.Background())

			got := mustGetSession(t, st)
			if got.Title != tt.wantTitle || got.Game != tt.wantGame {
				t.Errorf("session title, game = %q, %q, want %q, %q", got.Title, got.Game, tt.wantTitle, tt.wantGame)
			}
			if got.Version != tt.wantVersion {
				t.Errorf("session version = %d, want %d", got.Version, tt.wantVersion)
			}
		})
	}
}

func TestPollKicks(t *testing.T) {
	tests := []struct {
		name      string
		snapshot  *domain.Snapshot
		recording *domain.Recording
		starting  *domain.Session

		wantKicks int
	}{
		{
			name:      "went live kicks",
			snapshot:  liveSnapshot(),
			wantKicks: 1,
		},
		{
			name:     "stays live does not kick",
			snapshot: liveSnapshot(),
			starting: liveSession(),
		},
		{
			name:      "title change kicks",
			snapshot:  &domain.Snapshot{StreamID: "s1", Title: "T2", Game: "G"},
			starting:  liveSession(),
			wantKicks: 1,
		},
		{
			name:      "offline kicks",
			starting:  liveSession(),
			wantKicks: 1,
		},
		{
			name:      "offline with recording kicks for ended and archived",
			starting:  liveSession(),
			recording: &domain.Recording{URL: "u"},
			wantKicks: 2,
		},
		{
			name:     "waiting for recording does not kick",
			starting: endedSession(5 * time.Minute),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &fakeSource{snapshot: tt.snapshot, recording: tt.recording}
			var deliveries []domain.Delivery
			if tt.starting != nil {
				// a stored session always comes with its live delivery
				deliveries = []domain.Delivery{liveDeliveryAt(1)}
			}
			st := newStore(t, tt.starting, deliveries...)

			kicks := 0
			newPoller(src, st, domain.EndPolicyEditInPlace, poll.WithKick(func() { kicks++ })).Poll(context.Background())

			if kicks != tt.wantKicks {
				t.Errorf("kicks = %d, want %d", kicks, tt.wantKicks)
			}
		})
	}
}

func TestPollRecording(t *testing.T) {
	tests := []struct {
		name      string
		starting  *domain.Session
		recording *domain.Recording

		wantSourceCalls []string
		wantStored      bool
		wantURL         string // expected stored recording URL, checked when the session is stored
		wantVersion     int    // expected stored session version, checked when the session is stored
		wantState       domain.SessionState
	}{
		{
			name:            "found recording is stored",
			starting:        endedSession(5 * time.Minute),
			recording:       &domain.Recording{URL: "u"},
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantStored:      true,
			wantURL:         "u",
			wantVersion:     2,
			wantState:       domain.SessionArchived,
		},
		{
			name: "stored recording is not fetched again",
			starting: func() *domain.Session {
				s := endedSession(5 * time.Minute)
				s.Recording = domain.Recording{URL: "u"}
				s.Version = 2
				s.State = domain.SessionArchived
				return s
			}(),
			wantSourceCalls: []string{"FetchStream"},
			wantStored:      true,
			wantURL:         "u",
			wantVersion:     2,
			wantState:       domain.SessionArchived,
		},
		{
			name:            "waiting for recording keeps version",
			starting:        endedSession(5 * time.Minute),
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantStored:      true,
			wantVersion:     1,
			wantState:       domain.SessionEnded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &fakeSource{recording: tt.recording}
			st := newStore(t, tt.starting, liveDeliveryAt(1))

			newPoller(src, st, domain.EndPolicyEditInPlace).Poll(context.Background())

			if !slices.Equal(src.calls, tt.wantSourceCalls) {
				t.Errorf("source calls = %v, want %v", src.calls, tt.wantSourceCalls)
			}
			if got := st.GetSession() != nil; got != tt.wantStored {
				t.Fatalf("session stored = %v, want %v", got, tt.wantStored)
			}
			if !tt.wantStored {
				return
			}

			s := mustGetSession(t, st)
			if s.Recording.URL != tt.wantURL {
				t.Errorf("recording URL = %q, want %q", s.Recording.URL, tt.wantURL)
			}
			if s.Version != tt.wantVersion {
				t.Errorf("version = %d, want %d", s.Version, tt.wantVersion)
			}
			if s.State != tt.wantState {
				t.Errorf("state = %q, want %q", s.State, tt.wantState)
			}
		})
	}
}
