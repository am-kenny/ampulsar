package reconcile_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/am-kenny/ampulsar/internal/domain"
	"github.com/am-kenny/ampulsar/internal/reconcile"
	"github.com/am-kenny/ampulsar/internal/store"
)

type fakeSink struct {
	calls   []string
	sendRef domain.MessageRef
	errOn   map[string]error
}

func newFakeSink() *fakeSink {
	return &fakeSink{sendRef: domain.MessageRef{ID: "100", ChatID: "chat"}, errOn: map[string]error{}}
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

var testNow = time.Date(2026, 9, 17, 20, 0, 0, 0, time.UTC)

func newReconciler(sink reconcile.Sink, st reconcile.Store, onEnd domain.EndPolicy, pin, editOnChange bool) *reconcile.Reconciler {
	return reconcile.NewReconciler(sink, st,
		reconcile.Config{
			ChatID: "chat", Pin: pin, EditOnChange: editOnChange, OnEnd: onEnd,
			Style: "default", Lang: "eng", Interval: time.Minute,
		},
		reconcile.WithClock(func() time.Time { return testNow }),
	)
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

// finishedSession returns a session that ended a minute before testNow, in the given state
func finishedSession(state domain.SessionState) *domain.Session {
	s := liveSession()
	s.State = state
	s.EndedAt = testNow.Add(-time.Minute)
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

func doneDeliveryAt(version int) domain.Delivery {
	d := liveDeliveryAt(version)
	d.State = domain.DeliveryDone
	return d
}

// waitFor polls cond until it holds or a second has passed
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within a second")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSync(t *testing.T) {
	tests := []struct {
		name        string
		session     *domain.Session
		deliveries  []domain.Delivery
		onEnd       domain.EndPolicy
		pin         bool
		sendFails   bool
		deleteFails bool
		editFails   bool

		wantSinkCalls []string
		wantStored    bool                 // a live delivery is stored afterwards
		wantState     domain.DeliveryState // checked when a live delivery is stored
		wantSynced    int                  // checked when a live delivery is stored
	}{
		{
			name:  "no session does nothing",
			onEnd: domain.EndPolicyEditInPlace,
		},
		{
			name:          "went live",
			session:       liveSession(),
			onEnd:         domain.EndPolicyEditInPlace,
			wantSinkCalls: []string{"Send"},
			wantStored:    true,
			wantState:     domain.DeliveryPublished,
			wantSynced:    1,
		},
		{
			name:          "went live with pin",
			session:       liveSession(),
			onEnd:         domain.EndPolicyEditInPlace,
			pin:           true,
			wantSinkCalls: []string{"Send", "Pin"},
			wantStored:    true,
			wantState:     domain.DeliveryPublished,
			wantSynced:    1,
		},
		{
			name:          "went live but send fails",
			session:       liveSession(),
			onEnd:         domain.EndPolicyEditInPlace,
			pin:           true,
			sendFails:     true,
			wantSinkCalls: []string{"Send"},
		},
		{
			name:       "stays live",
			session:    liveSession(),
			deliveries: []domain.Delivery{liveDeliveryAt(1)},
			onEnd:      domain.EndPolicyEditInPlace,
			wantStored: true,
			wantState:  domain.DeliveryPublished,
			wantSynced: 1,
		},
		{
			name:          "offline edit in place",
			session:       finishedSession(domain.SessionArchived),
			deliveries:    []domain.Delivery{liveDeliveryAt(1)},
			onEnd:         domain.EndPolicyEditInPlace,
			wantSinkCalls: []string{"Edit"},
			wantStored:    true,
			wantState:     domain.DeliveryDone,
			wantSynced:    1,
		},
		{
			name:          "offline edit in place fails keeps delivery pending",
			session:       finishedSession(domain.SessionArchived),
			deliveries:    []domain.Delivery{liveDeliveryAt(1)},
			onEnd:         domain.EndPolicyEditInPlace,
			editFails:     true,
			wantSinkCalls: []string{"Edit"},
			wantStored:    true,
			wantState:     domain.DeliveryPublished,
			wantSynced:    1,
		},
		{
			name:          "offline new message",
			session:       finishedSession(domain.SessionArchived),
			deliveries:    []domain.Delivery{liveDeliveryAt(1)},
			onEnd:         domain.EndPolicyNewMessage,
			wantSinkCalls: []string{"Send"},
			wantStored:    true,
			wantState:     domain.DeliveryDone,
			wantSynced:    1,
		},
		{
			name:          "offline delete",
			session:       finishedSession(domain.SessionClosed),
			deliveries:    []domain.Delivery{liveDeliveryAt(1)},
			onEnd:         domain.EndPolicyDelete,
			wantSinkCalls: []string{"Delete"},
			wantStored:    true,
			wantState:     domain.DeliveryDone,
			wantSynced:    1,
		},
		{
			name:          "offline delete fails keeps delivery pending",
			session:       finishedSession(domain.SessionClosed),
			deliveries:    []domain.Delivery{liveDeliveryAt(1)},
			onEnd:         domain.EndPolicyDelete,
			deleteFails:   true,
			wantSinkCalls: []string{"Delete"},
			wantStored:    true,
			wantState:     domain.DeliveryPublished,
			wantSynced:    1,
		},
		{
			name:       "offline none",
			session:    finishedSession(domain.SessionClosed),
			deliveries: []domain.Delivery{liveDeliveryAt(1)},
			onEnd:      domain.EndPolicyNone,
			wantStored: true,
			wantState:  domain.DeliveryDone,
			wantSynced: 1,
		},
		{
			name:          "offline delete with pin unpins first",
			session:       finishedSession(domain.SessionClosed),
			deliveries:    []domain.Delivery{pinnedDeliveryAt(1)},
			onEnd:         domain.EndPolicyDelete,
			pin:           true,
			wantSinkCalls: []string{"Unpin", "Delete"},
			wantStored:    true,
			wantState:     domain.DeliveryDone,
			wantSynced:    1,
		},
		{
			name:          "offline replace deletes then posts",
			session:       finishedSession(domain.SessionArchived),
			deliveries:    []domain.Delivery{liveDeliveryAt(1)},
			onEnd:         domain.EndPolicyReplace,
			wantSinkCalls: []string{"Delete", "Send"},
			wantStored:    true,
			wantState:     domain.DeliveryDone,
			wantSynced:    1,
		},
		{
			name:       "offline replace waits for recording",
			session:    finishedSession(domain.SessionEnded),
			deliveries: []domain.Delivery{liveDeliveryAt(1)},
			onEnd:      domain.EndPolicyReplace,
			wantStored: true,
			wantState:  domain.DeliveryPublished,
			wantSynced: 1,
		},
		{
			name:          "offline replace delete fails does not post",
			session:       finishedSession(domain.SessionArchived),
			deliveries:    []domain.Delivery{liveDeliveryAt(1)},
			onEnd:         domain.EndPolicyReplace,
			deleteFails:   true,
			wantSinkCalls: []string{"Delete"},
			wantStored:    true,
			wantState:     domain.DeliveryPublished,
			wantSynced:    1,
		},
		{
			name:          "offline replace send fails keeps delivery pending",
			session:       finishedSession(domain.SessionArchived),
			deliveries:    []domain.Delivery{liveDeliveryAt(1)},
			onEnd:         domain.EndPolicyReplace,
			sendFails:     true,
			wantSinkCalls: []string{"Delete", "Send"},
			wantStored:    true,
			wantState:     domain.DeliveryPublished,
			wantSynced:    1,
		},
		{
			name:       "offline no recording waits",
			session:    finishedSession(domain.SessionEnded),
			deliveries: []domain.Delivery{liveDeliveryAt(1)},
			onEnd:      domain.EndPolicyEditInPlace,
			wantStored: true,
			wantState:  domain.DeliveryPublished,
			wantSynced: 1,
		},
		{
			name:          "closed without recording ends anyway",
			session:       finishedSession(domain.SessionClosed),
			deliveries:    []domain.Delivery{liveDeliveryAt(1)},
			onEnd:         domain.EndPolicyEditInPlace,
			wantSinkCalls: []string{"Edit"},
			wantStored:    true,
			wantState:     domain.DeliveryDone,
			wantSynced:    1,
		},
		{
			name:    "ended before announced does nothing",
			session: finishedSession(domain.SessionClosed),
			onEnd:   domain.EndPolicyEditInPlace,
		},
		{
			name:       "done does nothing",
			session:    finishedSession(domain.SessionArchived),
			deliveries: []domain.Delivery{doneDeliveryAt(1)},
			onEnd:      domain.EndPolicyEditInPlace,
			wantStored: true,
			wantState:  domain.DeliveryDone,
			wantSynced: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := newFakeSink()
			if tt.sendFails {
				sink.errOn["Send"] = errors.New("boom")
			}
			if tt.deleteFails {
				sink.errOn["Delete"] = errors.New("boom")
			}
			if tt.editFails {
				sink.errOn["Edit"] = errors.New("boom")
			}
			st := newStore(t, tt.session, tt.deliveries...)

			newReconciler(sink, st, tt.onEnd, tt.pin, false).Sync(context.Background())

			if !slices.Equal(sink.calls, tt.wantSinkCalls) {
				t.Errorf("sink calls = %v, want %v", sink.calls, tt.wantSinkCalls)
			}
			if got := len(st.GetDeliveries()) > 0; got != tt.wantStored {
				t.Fatalf("live delivery stored = %v, want %v", got, tt.wantStored)
			}
			if !tt.wantStored {
				return
			}

			live := mustLiveDelivery(t, st)
			if live.State != tt.wantState {
				t.Errorf("delivery state = %q, want %q", live.State, tt.wantState)
			}
			if live.SyncedVersion != tt.wantSynced {
				t.Errorf("synced version = %d, want %d", live.SyncedVersion, tt.wantSynced)
			}
		})
	}
}

func TestSyncStreamInfoChange(t *testing.T) {
	tests := []struct {
		name         string
		version      int               // session version
		deliveries   []domain.Delivery // starting deliveries
		editOnChange bool
		editFails    bool

		wantSinkCalls []string
		wantSynced    int // expected synced version
	}{
		{
			name:         "synced does nothing",
			version:      1,
			deliveries:   []domain.Delivery{liveDeliveryAt(1)},
			editOnChange: true,
			wantSynced:   1,
		},
		{
			name:          "change edits live message",
			version:       2,
			deliveries:    []domain.Delivery{liveDeliveryAt(1)},
			editOnChange:  true,
			wantSinkCalls: []string{"Edit"},
			wantSynced:    2,
		},
		{
			name:       "change without edit on change leaves delivery behind",
			version:    2,
			deliveries: []domain.Delivery{liveDeliveryAt(1)},
			wantSynced: 1,
		},
		{
			name:          "failed edit leaves delivery behind",
			version:       2,
			deliveries:    []domain.Delivery{liveDeliveryAt(1)},
			editOnChange:  true,
			editFails:     true,
			wantSinkCalls: []string{"Edit"},
			wantSynced:    1,
		},
		{
			name:          "delivery several versions behind catches up",
			version:       3,
			deliveries:    []domain.Delivery{liveDeliveryAt(1)},
			editOnChange:  true,
			wantSinkCalls: []string{"Edit"},
			wantSynced:    3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := newFakeSink()
			if tt.editFails {
				sink.errOn["Edit"] = errors.New("boom")
			}
			session := liveSession()
			session.Version = tt.version
			st := newStore(t, session, tt.deliveries...)

			newReconciler(sink, st, domain.EndPolicyEditInPlace, false, tt.editOnChange).Sync(context.Background())

			if !slices.Equal(sink.calls, tt.wantSinkCalls) {
				t.Errorf("sink calls = %v, want %v", sink.calls, tt.wantSinkCalls)
			}
			if synced := mustLiveDelivery(t, st).SyncedVersion; synced != tt.wantSynced {
				t.Errorf("synced version = %d, want %d", synced, tt.wantSynced)
			}
		})
	}
}

func TestSyncPin(t *testing.T) {
	tests := []struct {
		name         string
		session      *domain.Session
		delivery     domain.Delivery
		pin          bool
		editOnChange bool
		pinFails     bool
		unpinFails   bool

		wantSinkCalls []string
		wantPinned    bool
		wantSynced    int
	}{
		{
			name:          "live and not pinned retries pin",
			session:       liveSession(),
			delivery:      liveDeliveryAt(1),
			pin:           true,
			wantSinkCalls: []string{"Pin"},
			wantPinned:    true,
			wantSynced:    1,
		},
		{
			name:       "live and already pinned does nothing",
			session:    liveSession(),
			delivery:   pinnedDeliveryAt(1),
			pin:        true,
			wantPinned: true,
			wantSynced: 1,
		},
		{
			name: "live pin and edit keep each other's changes",
			session: func() *domain.Session {
				s := liveSession()
				s.Version = 2
				return s
			}(),
			delivery:      liveDeliveryAt(1),
			pin:           true,
			editOnChange:  true,
			wantSinkCalls: []string{"Pin", "Edit"},
			wantPinned:    true,
			wantSynced:    2,
		},
		{
			name: "live pin fails does not block edit",
			session: func() *domain.Session {
				s := liveSession()
				s.Version = 2
				return s
			}(),
			delivery:      liveDeliveryAt(1),
			pin:           true,
			editOnChange:  true,
			pinFails:      true,
			wantSinkCalls: []string{"Pin", "Edit"},
			wantSynced:    2,
		},
		{
			name:          "offline unpins pinned message without pin config",
			session:       finishedSession(domain.SessionArchived),
			delivery:      pinnedDeliveryAt(1),
			wantSinkCalls: []string{"Unpin", "Edit"},
			wantSynced:    1,
		},
		{
			name:          "offline unpin fails holds end action",
			session:       finishedSession(domain.SessionArchived),
			delivery:      pinnedDeliveryAt(1),
			pin:           true,
			unpinFails:    true,
			wantSinkCalls: []string{"Unpin"},
			wantPinned:    true,
			wantSynced:    1,
		},
		{
			name:          "ended unpins while waiting for recording",
			session:       finishedSession(domain.SessionEnded),
			delivery:      pinnedDeliveryAt(1),
			pin:           true,
			wantSinkCalls: []string{"Unpin"},
			wantSynced:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := newFakeSink()
			if tt.pinFails {
				sink.errOn["Pin"] = errors.New("boom")
			}
			if tt.unpinFails {
				sink.errOn["Unpin"] = errors.New("boom")
			}
			st := newStore(t, tt.session, tt.delivery)

			newReconciler(sink, st, domain.EndPolicyEditInPlace, tt.pin, tt.editOnChange).Sync(context.Background())

			if !slices.Equal(sink.calls, tt.wantSinkCalls) {
				t.Errorf("sink calls = %v, want %v", sink.calls, tt.wantSinkCalls)
			}

			live := mustLiveDelivery(t, st)
			if live.Pinned != tt.wantPinned {
				t.Errorf("pinned = %v, want %v", live.Pinned, tt.wantPinned)
			}
			if live.SyncedVersion != tt.wantSynced {
				t.Errorf("synced version = %d, want %d", live.SyncedVersion, tt.wantSynced)
			}
		})
	}
}

func TestKickNeverBlocks(t *testing.T) {
	r := newReconciler(newFakeSink(), &store.Store{}, domain.EndPolicyEditInPlace, false, false)

	done := make(chan struct{})
	go func() {
		r.Kick()
		r.Kick()
		r.Kick()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Kick blocked without a running loop")
	}
}

func TestRunSyncsOnKick(t *testing.T) {
	st := newStore(t, liveSession())

	// an interval of an hour, so only the first sync and kicks can send
	r := reconcile.NewReconciler(newFakeSink(), st, reconcile.Config{
		ChatID: "chat", Style: "default", Lang: "eng", Interval: time.Hour,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()

	waitFor(t, func() bool { return len(st.GetDeliveries()) == 1 })

	// a new stream replaces the session and its deliveries; only the kick can publish it
	next := liveSession()
	next.StreamID = "s2"
	if err := st.SetSession(*next); err != nil {
		t.Fatalf("SetSession: %v", err)
	}
	r.Kick()

	waitFor(t, func() bool {
		d := st.GetDeliveries()
		return len(d) == 1 && d[0].StreamID == "s2"
	})

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
