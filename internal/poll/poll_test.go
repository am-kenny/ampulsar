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
			WaitForRecording: onEnd.NeedsRecording(),
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

func pinnedDeliveryAt(version int) domain.Delivery {
	d := liveDeliveryAt(version)
	d.Pinned = true
	return d
}

func TestPoll(t *testing.T) {
	tests := []struct {
		name        string
		snapshot    *domain.Snapshot
		recording   *domain.Recording
		starting    *domain.Session
		deliveries  []domain.Delivery
		onEnd       domain.EndPolicy
		pin         bool
		sendFails   bool
		deleteFails bool

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
			deliveries:      []domain.Delivery{pinnedDeliveryAt(1)},
			onEnd:           domain.EndPolicyDelete,
			pin:             true,
			wantSourceCalls: []string{"FetchStream"},
			wantSinkCalls:   []string{"Unpin", "Delete"},
		},
		{
			name:            "offline replace deletes then posts",
			starting:        liveSession(),
			recording:       &domain.Recording{URL: "u"},
			onEnd:           domain.EndPolicyReplace,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantSinkCalls:   []string{"Delete", "Send"},
		},
		{
			name:            "offline replace waits for recording",
			starting:        liveSession(),
			onEnd:           domain.EndPolicyReplace,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantStored:      true,
		},
		{
			name:            "offline replace delete fails does not post",
			starting:        liveSession(),
			deliveries:      []domain.Delivery{liveDeliveryAt(1)},
			recording:       &domain.Recording{URL: "u"},
			onEnd:           domain.EndPolicyReplace,
			deleteFails:     true,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantSinkCalls:   []string{"Delete"},
			wantStored:      true,
		},
		{
			name:            "offline replace send fails keeps session",
			starting:        liveSession(),
			deliveries:      []domain.Delivery{liveDeliveryAt(1)},
			recording:       &domain.Recording{URL: "u"},
			onEnd:           domain.EndPolicyReplace,
			sendFails:       true,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantSinkCalls:   []string{"Delete", "Send"},
			wantStored:      true,
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
			sink.errOn = map[string]error{}
			if tt.sendFails {
				sink.errOn["Send"] = errors.New("boom")
			}
			if tt.deleteFails {
				sink.errOn["Delete"] = errors.New("boom")
			}
			deliveries := tt.deliveries
			if tt.starting != nil && deliveries == nil {
				// a stored session always comes with its live delivery
				deliveries = []domain.Delivery{liveDeliveryAt(1)}
			}
			st := newStore(t, tt.starting, deliveries...)

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
				live := mustLiveDelivery(t, st)
				if live.SyncedVersion != tt.wantSynced {
					t.Errorf("synced version = %d, want %d", live.SyncedVersion, tt.wantSynced)
				}
				if live.Pinned != tt.pin {
					t.Errorf("pinned = %v, want %v", live.Pinned, tt.pin)
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

func TestPollPin(t *testing.T) {
	tests := []struct {
		name       string
		snapshot   *domain.Snapshot
		starting   *domain.Session
		delivery   domain.Delivery
		pin        bool
		unpinFails bool

		wantSinkCalls []string
		wantStored    bool
		wantPinned    bool // checked when the session is stored
	}{
		{
			name:          "live and not pinned retries pin",
			snapshot:      liveSnapshot(),
			starting:      liveSession(),
			delivery:      liveDeliveryAt(1),
			pin:           true,
			wantSinkCalls: []string{"Pin"},
			wantStored:    true,
			wantPinned:    true,
		},
		{
			name:       "live and already pinned does nothing",
			snapshot:   liveSnapshot(),
			starting:   liveSession(),
			delivery:   pinnedDeliveryAt(1),
			pin:        true,
			wantStored: true,
			wantPinned: true,
		},
		{
			name:          "offline unpins pinned message without pin config",
			starting:      liveSession(),
			delivery:      pinnedDeliveryAt(1),
			wantSinkCalls: []string{"Unpin", "Edit"},
		},
		{
			name:          "offline unpin fails holds end action",
			starting:      liveSession(),
			delivery:      pinnedDeliveryAt(1),
			pin:           true,
			unpinFails:    true,
			wantSinkCalls: []string{"Unpin"},
			wantStored:    true,
			wantPinned:    true,
		},
		{
			name:          "ended within grace retries unpin",
			starting:      endedSession(5 * time.Minute),
			delivery:      pinnedDeliveryAt(1),
			pin:           true,
			wantSinkCalls: []string{"Unpin", "Edit"},
		},
		{
			name:          "ended past grace with unpin failing finalizes",
			starting:      endedSession(11 * time.Minute),
			delivery:      pinnedDeliveryAt(1),
			pin:           true,
			unpinFails:    true,
			wantSinkCalls: []string{"Unpin", "Edit"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &fakeSource{snapshot: tt.snapshot, recording: &domain.Recording{URL: "u"}}
			sink := newFakeSink()
			sink.errOn = map[string]error{}
			if tt.unpinFails {
				sink.errOn["Unpin"] = errors.New("boom")
			}
			st := newStore(t, tt.starting, tt.delivery)

			newPoller(src, sink, st, domain.EndPolicyEditInPlace, tt.pin, false).Poll(context.Background())

			if !slices.Equal(sink.calls, tt.wantSinkCalls) {
				t.Errorf("sink calls = %v, want %v", sink.calls, tt.wantSinkCalls)
			}
			if got := st.GetSession() != nil; got != tt.wantStored {
				t.Fatalf("session stored = %v, want %v", got, tt.wantStored)
			}
			if !tt.wantStored {
				return
			}

			if got := mustLiveDelivery(t, st).Pinned; got != tt.wantPinned {
				t.Errorf("pinned = %v, want %v", got, tt.wantPinned)
			}
		})
	}
}

func TestPollRecording(t *testing.T) {
	tests := []struct {
		name      string
		starting  *domain.Session
		recording *domain.Recording
		editFails bool

		wantSourceCalls []string
		wantSinkCalls   []string
		wantStored      bool
		wantURL         string // expected stored recording URL, checked when the session is stored
		wantVersion     int    // expected stored session version, checked when the session is stored
		wantState       domain.SessionState
	}{
		{
			name:            "found recording is stored when end action fails",
			starting:        endedSession(5 * time.Minute),
			recording:       &domain.Recording{URL: "u"},
			editFails:       true,
			wantSourceCalls: []string{"FetchStream", "FetchRecording"},
			wantSinkCalls:   []string{"Edit"},
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
			wantSinkCalls:   []string{"Edit"},
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
			sink := newFakeSink()
			sink.errOn = map[string]error{}
			if tt.editFails {
				sink.errOn["Edit"] = errors.New("boom")
			}
			st := newStore(t, tt.starting, liveDeliveryAt(1))

			newPoller(src, sink, st, domain.EndPolicyEditInPlace, false, false).Poll(context.Background())

			if !slices.Equal(src.calls, tt.wantSourceCalls) {
				t.Errorf("source calls = %v, want %v", src.calls, tt.wantSourceCalls)
			}
			if !slices.Equal(sink.calls, tt.wantSinkCalls) {
				t.Errorf("sink calls = %v, want %v", sink.calls, tt.wantSinkCalls)
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
