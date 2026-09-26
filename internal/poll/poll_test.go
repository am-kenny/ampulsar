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

type fakeSink struct {
	calls   []string
	sendRef domain.MessageRef
	errOn   map[string]error
}

func newFakeSink() *fakeSink {
	return &fakeSink{sendRef: domain.MessageRef{ID: "100", ChatID: "chat"}}
}

func (f *fakeSink) record(method string) error {
	f.calls = append(f.calls, method)
	return f.errOn[method]
}

func (f *fakeSink) Send(_ context.Context, _, _ string) (domain.MessageRef, error) {
	if err := f.record("Send"); err != nil {
		return domain.MessageRef{}, err
	}
	return f.sendRef, nil
}

func (f *fakeSink) Edit(_ context.Context, _ domain.MessageRef, _ string) error {
	return f.record("Edit")
}

func (f *fakeSink) Delete(_ context.Context, _ domain.MessageRef) error {
	return f.record("Delete")
}

func (f *fakeSink) Pin(_ context.Context, _ domain.MessageRef) error {
	return f.record("Pin")
}

func (f *fakeSink) Unpin(_ context.Context, _ domain.MessageRef) error {
	return f.record("Unpin")
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

// mustLiveDelivery returns the stored live delivery and fails the test if there is none
func mustLiveDelivery(t *testing.T, st *store.Store) domain.Delivery {
	t.Helper()

	for _, d := range st.GetDeliveries() {
		if d.Kind == domain.DeliveryLive {
			return d
		}
	}

	t.Fatal("no live delivery stored")
	return domain.Delivery{}
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

func newPoller(src poll.Source, sink poll.Sink, st poll.Store, onEnd domain.EndPolicy, pin, editOnChange bool) *poll.Poller {
	return poll.NewPoller(src, sink, st,
		domain.Channel{Platform: domain.Twitch, ID: "u1", Username: "streamer"},
		poll.Config{
			ChatID: "chat", Pin: pin, EditOnChange: editOnChange, OnEnd: onEnd,
			Style: "default", Lang: "eng", EndGrace: 10 * time.Minute,
		},
		poll.WithClock(func() time.Time { return testNow }),
	)
}

func liveSnapshot() *domain.Snapshot {
	return &domain.Snapshot{StreamID: "s1", Title: "T", Game: "G"}
}

func liveSession() *domain.Session {
	return &domain.Session{
		Platform: domain.Twitch, ID: "u1",
		Username:    "streamer",
		StreamID:    "s1",
		State:       domain.SessionLive,
		Version:     1,
		LiveMessage: domain.MessageRef{ID: "100", ChatID: "chat"},
		Title:       "T",
		Game:        "G",
		URL:         "https://www.twitch.tv/streamer",
	}
}

func endedSession(ago time.Duration) *domain.Session {
	s := liveSession()
	s.State = domain.SessionEnded
	s.EndedAt = testNow.Add(-ago)
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

func TestPoll(t *testing.T) {
	tests := []struct {
		name      string
		snapshot  *domain.Snapshot
		recording *domain.Recording
		starting  *domain.Session
		onEnd     domain.EndPolicy
		pin       bool
		sendFails bool

		wantSourceCalls []string
		wantSinkCalls   []string
		wantStored      bool
		wantSynced      int
		recordingErr    error
	}{
		{
			name:            "stays live",
			snapshot:        liveSnapshot(),
			starting:        liveSession(),
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream"},
			wantStored:      true,
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
			wantSinkCalls:   []string{"Send"},
			wantStored:      true,
			wantSynced:      1,
		},
		{
			name:            "went live with pin",
			snapshot:        liveSnapshot(),
			onEnd:           domain.EndPolicyEditInPlace,
			pin:             true,
			wantSourceCalls: []string{"FetchStream"},
			wantSinkCalls:   []string{"Send", "Pin"},
			wantStored:      true,
			wantSynced:      1,
		},
		{
			name:            "went live but send fails",
			snapshot:        liveSnapshot(),
			onEnd:           domain.EndPolicyEditInPlace,
			sendFails:       true,
			wantSourceCalls: []string{"FetchStream"},
			wantSinkCalls:   []string{"Send"},
		},
		{
			name:            "offline edit in place",
			starting:        liveSession(),
			recording:       &domain.Recording{URL: "u"},
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantSinkCalls:   []string{"Edit"},
		},
		{
			name:            "offline new message",
			starting:        liveSession(),
			recording:       &domain.Recording{URL: "u"},
			onEnd:           domain.EndPolicyNewMessage,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantSinkCalls:   []string{"Send"},
		},
		{
			name:            "offline delete",
			starting:        liveSession(),
			onEnd:           domain.EndPolicyDelete,
			wantSourceCalls: []string{"FetchStream"},
			wantSinkCalls:   []string{"Delete"},
		},
		{
			name:            "offline none",
			starting:        liveSession(),
			onEnd:           domain.EndPolicyNone,
			wantSourceCalls: []string{"FetchStream"},
		},
		{
			name:            "offline delete with pin unpins first",
			starting:        liveSession(),
			onEnd:           domain.EndPolicyDelete,
			pin:             true,
			wantSourceCalls: []string{"FetchStream"},
			wantSinkCalls:   []string{"Unpin", "Delete"},
		},
		{
			name:            "offline no recording keeps session",
			starting:        liveSession(),
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantStored:      true,
		},
		{
			name:            "offline source without recordings finalizes immediately",
			starting:        liveSession(),
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantSinkCalls:   []string{"Edit"},
			recordingErr:    domain.ErrNoRecordings,
		},
		{
			name:            "ended past grace without recording finalizes",
			starting:        endedSession(11 * time.Minute),
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantSinkCalls:   []string{"Edit"},
		},
		{
			name:            "ended within grace with fetch error keeps session",
			starting:        endedSession(5 * time.Minute),
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantStored:      true,
			recordingErr:    errors.New("unknown"),
		},
		{
			name:            "ended past grace with fetch error finalizes",
			starting:        endedSession(11 * time.Minute),
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantSinkCalls:   []string{"Edit"},
			recordingErr:    errors.New("unknown"),
		},
		{
			name:            "stream restarted finalizes old session",
			snapshot:        &domain.Snapshot{StreamID: "s2", Title: "T", Game: "G"},
			starting:        liveSession(),
			recording:       &domain.Recording{URL: "u"},
			onEnd:           domain.EndPolicyEditInPlace,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantSinkCalls:   []string{"Edit"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &fakeSource{snapshot: tt.snapshot, recording: tt.recording, recordingErr: tt.recordingErr}
			sink := newFakeSink()
			if tt.sendFails {
				sink.errOn = map[string]error{"Send": errors.New("boom")}
			}
			st := newStore(t, tt.starting)

			newPoller(src, sink, st, tt.onEnd, tt.pin, false).Poll(context.Background())

			if !slices.Equal(src.calls, tt.wantSourceCalls) {
				t.Errorf("source calls = %v, want %v", src.calls, tt.wantSourceCalls)
			}
			if !slices.Equal(sink.calls, tt.wantSinkCalls) {
				t.Errorf("sink calls = %v, want %v", sink.calls, tt.wantSinkCalls)
			}
			if got := st.GetSession() != nil; got != tt.wantStored {
				t.Errorf("session stored = %v, want %v", got, tt.wantStored)
			}
			if tt.wantSynced != 0 {
				if got := mustLiveDelivery(t, st).SyncedVersion; got != tt.wantSynced {
					t.Errorf("synced version = %d, want %d", got, tt.wantSynced)
				}
			}
		})
	}
}

func TestPollStreamInfoChange(t *testing.T) {
	tests := []struct {
		name         string
		snapshot     *domain.Snapshot
		version      int               // starting session version
		deliveries   []domain.Delivery // starting deliveries
		editOnChange bool
		editFails    bool

		wantSinkCalls []string
		wantTitle     string
		wantGame      string
		wantVersion   int
		wantSynced    int // expected SyncedVersion for delivery of type Live , 0 = not checked
	}{
		{
			name:         "unchanged and synced does nothing",
			snapshot:     liveSnapshot(),
			version:      1,
			deliveries:   []domain.Delivery{liveDeliveryAt(1)},
			editOnChange: true,
			wantTitle:    "T",
			wantGame:     "G",
			wantVersion:  1,
			wantSynced:   1,
		},
		{
			name:          "title change edits live message",
			snapshot:      &domain.Snapshot{StreamID: "s1", Title: "T2", Game: "G"},
			version:       1,
			deliveries:    []domain.Delivery{liveDeliveryAt(1)},
			editOnChange:  true,
			wantSinkCalls: []string{"Edit"},
			wantTitle:     "T2",
			wantGame:      "G",
			wantVersion:   2,
			wantSynced:    2,
		},
		{
			name:          "game change edits live message",
			snapshot:      &domain.Snapshot{StreamID: "s1", Title: "T", Game: "G2"},
			version:       1,
			deliveries:    []domain.Delivery{liveDeliveryAt(1)},
			editOnChange:  true,
			wantSinkCalls: []string{"Edit"},
			wantTitle:     "T",
			wantGame:      "G2",
			wantVersion:   2,
			wantSynced:    2,
		},
		{
			name:        "change without edit on change only updates session",
			snapshot:    &domain.Snapshot{StreamID: "s1", Title: "T2", Game: "G2"},
			version:     1,
			deliveries:  []domain.Delivery{liveDeliveryAt(1)},
			wantTitle:   "T2",
			wantGame:    "G2",
			wantVersion: 2,
			wantSynced:  1,
		},
		{
			name:          "failed edit stores session and leaves delivery behind",
			snapshot:      &domain.Snapshot{StreamID: "s1", Title: "T2", Game: "G"},
			version:       1,
			deliveries:    []domain.Delivery{liveDeliveryAt(1)},
			editOnChange:  true,
			editFails:     true,
			wantSinkCalls: []string{"Edit"},
			wantTitle:     "T2",
			wantGame:      "G",
			wantVersion:   2,
			wantSynced:    1,
		},
		{
			name:          "unchanged but delivery behind catches up",
			snapshot:      liveSnapshot(),
			version:       2,
			deliveries:    []domain.Delivery{liveDeliveryAt(1)},
			editOnChange:  true,
			wantSinkCalls: []string{"Edit"},
			wantTitle:     "T",
			wantGame:      "G",
			wantVersion:   2,
			wantSynced:    2,
		},
		{
			name:          "missing live delivery is rebuilt from session",
			snapshot:      liveSnapshot(),
			version:       1,
			editOnChange:  true,
			wantSinkCalls: []string{"Edit"},
			wantTitle:     "T",
			wantGame:      "G",
			wantVersion:   1,
			wantSynced:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &fakeSource{snapshot: tt.snapshot}
			sink := newFakeSink()
			if tt.editFails {
				sink.errOn = map[string]error{"Edit": errors.New("boom")}
			}
			session := liveSession()
			session.Version = tt.version
			st := newStore(t, session, tt.deliveries...)

			newPoller(src, sink, st, domain.EndPolicyEditInPlace, false, tt.editOnChange).Poll(context.Background())

			if !slices.Equal(sink.calls, tt.wantSinkCalls) {
				t.Errorf("sink calls = %v, want %v", sink.calls, tt.wantSinkCalls)
			}

			got := mustGetSession(t, st)
			if got.Title != tt.wantTitle || got.Game != tt.wantGame {
				t.Errorf("session title, game = %q, %q, want %q, %q", got.Title, got.Game, tt.wantTitle, tt.wantGame)
			}
			if got.Version != tt.wantVersion {
				t.Errorf("session version = %d, want %d", got.Version, tt.wantVersion)
			}
			if synced := mustLiveDelivery(t, st).SyncedVersion; synced != tt.wantSynced {
				t.Errorf("synced version = %d, want %d", synced, tt.wantSynced)
			}
		})
	}
}
