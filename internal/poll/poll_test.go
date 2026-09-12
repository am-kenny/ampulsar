package poll_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/am-kenny/ampulsar/internal/domain"
	"github.com/am-kenny/ampulsar/internal/poll"
)

type fakeSource struct {
	snapshot  *domain.Snapshot
	recording *domain.Recording
}

func (f *fakeSource) FetchStream(_ context.Context, _ string) (*domain.Snapshot, error) {
	return f.snapshot, nil
}

func (f *fakeSource) FetchRecording(_ context.Context, _, _ string) (*domain.Recording, error) {
	return f.recording, nil
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

type fakeStore struct {
	session *domain.Session
}

func (f *fakeStore) GetSession() *domain.Session {
	if f.session == nil {
		return nil
	}
	cp := *f.session
	return &cp
}

func (f *fakeStore) SetSession(s domain.Session) error {
	f.session = &s
	return nil
}

func (f *fakeStore) DeleteSession() error {
	f.session = nil
	return nil
}

func newPoller(src poll.Source, sink poll.Sink, st poll.Store, onEnd domain.EndPolicy, pin bool) *poll.Poller {
	return poll.NewPoller(src, sink, st,
		domain.Channel{Platform: domain.Twitch, ID: "u1", Username: "streamer"},
		poll.Config{ChatID: "chat", Pin: pin, OnEnd: onEnd, Style: "default", Lang: "eng"},
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
		LiveMessage: domain.MessageRef{ID: "100", ChatID: "chat"},
		Title:       "T",
		Game:        "G",
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

		wantCalls  []string
		wantStored bool
	}{
		{
			name:       "stays live",
			snapshot:   liveSnapshot(),
			starting:   liveSession(),
			onEnd:      domain.EndPolicyEditInPlace,
			wantStored: true,
		},
		{
			name:  "stays offline",
			onEnd: domain.EndPolicyEditInPlace,
		},
		{
			name:       "went live",
			snapshot:   liveSnapshot(),
			onEnd:      domain.EndPolicyEditInPlace,
			wantCalls:  []string{"Send"},
			wantStored: true,
		},
		{
			name:       "went live with pin",
			snapshot:   liveSnapshot(),
			onEnd:      domain.EndPolicyEditInPlace,
			pin:        true,
			wantCalls:  []string{"Send", "Pin"},
			wantStored: true,
		},
		{
			name:      "went live but send fails",
			snapshot:  liveSnapshot(),
			onEnd:     domain.EndPolicyEditInPlace,
			sendFails: true,
			wantCalls: []string{"Send"},
		},
		{
			name:      "offline edit in place",
			starting:  liveSession(),
			recording: &domain.Recording{URL: "u"},
			onEnd:     domain.EndPolicyEditInPlace,
			wantCalls: []string{"Edit"},
		},
		{
			name:      "offline new message",
			starting:  liveSession(),
			recording: &domain.Recording{URL: "u"},
			onEnd:     domain.EndPolicyNewMessage,
			wantCalls: []string{"Send"},
		},
		{
			name:      "offline delete",
			starting:  liveSession(),
			onEnd:     domain.EndPolicyDelete,
			wantCalls: []string{"Delete"},
		},
		{
			name:     "offline none",
			starting: liveSession(),
			onEnd:    domain.EndPolicyNone,
		},
		{
			name:      "offline delete with pin unpins first",
			starting:  liveSession(),
			onEnd:     domain.EndPolicyDelete,
			pin:       true,
			wantCalls: []string{"Unpin", "Delete"},
		},
		{
			// No VOD yet: must not act or delete; retry next tick
			name:       "offline no recording keeps session",
			starting:   liveSession(),
			onEnd:      domain.EndPolicyEditInPlace,
			wantStored: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &fakeSource{snapshot: tt.snapshot, recording: tt.recording}
			sink := newFakeSink()
			if tt.sendFails {
				sink.errOn = map[string]error{"Send": errors.New("boom")}
			}
			st := &fakeStore{session: tt.starting}

			newPoller(src, sink, st, tt.onEnd, tt.pin).Poll(context.Background())

			if !slices.Equal(sink.calls, tt.wantCalls) {
				t.Errorf("calls = %v, want %v", sink.calls, tt.wantCalls)
			}
			if got := st.session != nil; got != tt.wantStored {
				t.Errorf("session stored = %v, want %v", got, tt.wantStored)
			}
		})
	}
}
