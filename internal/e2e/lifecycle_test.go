package e2e

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/am-kenny/ampulsar/internal/domain"
	"github.com/am-kenny/ampulsar/internal/poll"
	"github.com/am-kenny/ampulsar/internal/reconcile"
	"github.com/am-kenny/ampulsar/internal/store"
)

// source is a scripted stream source
type source struct {
	snapshot  *domain.Snapshot
	recording *domain.Recording
}

func (s *source) FetchStream(_ context.Context, _ domain.Channel) (*domain.Snapshot, error) {
	return s.snapshot, nil
}

func (s *source) FetchRecording(_ context.Context, _ domain.Channel, _ string) (*domain.Recording, error) {
	return s.recording, nil
}

// chat is a fake sink that keeps the messages of one chat and logs every call
type chat struct {
	nextID   int
	messages map[string]string // message id -> text
	order    []string          // message ids in the order they were sent
	pinned   map[string]bool
	calls    []string
}

func newChat() *chat {
	return &chat{messages: map[string]string{}, pinned: map[string]bool{}}
}

func (c *chat) Send(_ context.Context, chatID, text string) (domain.MessageRef, error) {
	c.nextID++
	id := strconv.Itoa(c.nextID)

	c.messages[id] = text
	c.order = append(c.order, id)
	c.calls = append(c.calls, "Send "+kindOf(text))

	return domain.MessageRef{ID: id, ChatID: chatID}, nil
}

func (c *chat) Edit(_ context.Context, ref domain.MessageRef, text string) error {
	if err := c.find(ref); err != nil {
		return err
	}

	c.messages[ref.ID] = text
	c.calls = append(c.calls, "Edit "+kindOf(text))
	return nil
}

func (c *chat) Delete(_ context.Context, ref domain.MessageRef) error {
	if err := c.find(ref); err != nil {
		return err
	}

	delete(c.messages, ref.ID)
	delete(c.pinned, ref.ID)
	c.calls = append(c.calls, "Delete")
	return nil
}

func (c *chat) Pin(_ context.Context, ref domain.MessageRef) error {
	if err := c.find(ref); err != nil {
		return err
	}

	c.pinned[ref.ID] = true
	c.calls = append(c.calls, "Pin")
	return nil
}

func (c *chat) Unpin(_ context.Context, ref domain.MessageRef) error {
	if err := c.find(ref); err != nil {
		return err
	}

	delete(c.pinned, ref.ID)
	c.calls = append(c.calls, "Unpin")
	return nil
}

func (c *chat) find(ref domain.MessageRef) error {
	if _, ok := c.messages[ref.ID]; !ok {
		return fmt.Errorf("chat: message %q not found", ref.ID)
	}
	return nil
}

// takeCalls returns the calls logged since the last take
func (c *chat) takeCalls() []string {
	calls := c.calls
	c.calls = nil
	return calls
}

// visible returns the kind of every message left in the chat, oldest first, marking pinned ones
func (c *chat) visible() []string {
	var out []string
	for _, id := range c.order {
		text, ok := c.messages[id]
		if !ok {
			continue
		}

		entry := kindOf(text)
		if c.pinned[id] {
			entry += " pinned"
		}
		out = append(out, entry)
	}
	return out
}

// texts returns the text of every message left in the chat
func (c *chat) texts() []string {
	var out []string
	for _, id := range c.order {
		if text, ok := c.messages[id]; ok {
			out = append(out, text)
		}
	}
	return out
}

// kindOf differentiates the live and offline messages
func kindOf(text string) string {
	switch {
	case strings.HasPrefix(text, "🔴"):
		return "live"
	case strings.HasPrefix(text, "⚫"):
		return "offline"
	}
	return "unknown"
}

var (
	start   = time.Date(2026, 9, 17, 19, 0, 0, 0, time.UTC)
	channel = domain.Channel{Platform: domain.Twitch, ID: "u1", Username: "streamer", DisplayName: "Streamer"}
)

const endGrace = 10 * time.Minute

// scenario runs poller and reconciler over a shared store and clock
type scenario struct {
	t          *testing.T
	now        time.Time
	source     *source
	store      *store.Store
	chat       *chat
	poller     *poll.Poller
	reconciler *reconcile.Reconciler
}

func newScenario(t *testing.T, onEnd domain.EndPolicy) *scenario {
	t.Helper()

	sc := &scenario{t: t, now: start, source: &source{}, store: &store.Store{}, chat: newChat()}
	clock := func() time.Time { return sc.now }

	sc.reconciler = reconcile.NewReconciler(sc.chat, sc.store,
		reconcile.Config{
			ChatID: "chat", Pin: true, EditOnChange: true, OnEnd: onEnd,
			Style: "default", Lang: "eng", Interval: time.Minute,
		},
		reconcile.WithClock(clock),
	)

	sc.poller = poll.NewPoller(sc.source, sc.store, channel,
		poll.Config{Interval: time.Minute, EndGrace: endGrace, WaitForRecording: onEnd.NeedsRecording()},
		poll.WithClock(clock),
	)

	return sc
}

// at moves the clock to the given minute after start, polls, syncs and checks the sink calls
func (sc *scenario) at(minute int, step string, want ...string) {
	sc.t.Helper()

	sc.now = start.Add(time.Duration(minute) * time.Minute)

	ctx := sc.t.Context()
	sc.poller.Poll(ctx)
	sc.reconciler.Sync(ctx)

	if got := sc.chat.takeCalls(); !slices.Equal(got, want) {
		sc.t.Errorf("%s (minute %d): calls = %q, want %q", step, minute, got, want)
	}
}

// settled checks that the session is gone and the chat shows want
func (sc *scenario) settled(want ...string) {
	sc.t.Helper()

	if session := sc.store.GetSession(); session != nil {
		sc.t.Errorf("session still stored: %+v", *session)
	}
	if got := sc.chat.visible(); !slices.Equal(got, want) {
		sc.t.Errorf("chat = %q, want %q", got, want)
	}
}

func snapshot(streamID, title string) *domain.Snapshot {
	return &domain.Snapshot{
		StreamID: streamID,
		Title:    title,
		Game:     "Factorio",
		URL:      "https://www.twitch.tv/streamer",
	}
}

func TestLifecycle(t *testing.T) {
	recording := &domain.Recording{
		URL:      "https://www.twitch.tv/videos/1",
		Title:    "Day 3 - trains",
		Duration: 3*time.Hour + 30*time.Minute,
	}

	tests := []struct {
		onEnd       domain.EndPolicy
		atEnd       []string // calls when the stream goes offline
		atRecording []string // calls when the recording is found
		chat        []string // messages in the chat
	}{
		{domain.EndPolicyEditInPlace, []string{"Unpin"}, []string{"Edit offline"}, []string{"offline"}},
		{domain.EndPolicyNewMessage, []string{"Unpin"}, []string{"Send offline"}, []string{"live", "offline"}},
		{domain.EndPolicyReplace, []string{"Unpin"}, []string{"Delete", "Send offline"}, []string{"offline"}},
		{domain.EndPolicyDelete, []string{"Unpin", "Delete"}, nil, nil},
		{domain.EndPolicyNone, []string{"Unpin"}, nil, []string{"live"}},
	}

	for _, tt := range tests {
		t.Run(string(tt.onEnd), func(t *testing.T) {
			sc := newScenario(t, tt.onEnd)

			sc.source.snapshot = snapshot("s1", "Day 3")
			sc.at(0, "go-live", "Send live", "Pin")
			sc.at(1, "idle")

			sc.source.snapshot = snapshot("s1", "Day 3 - trains")
			sc.at(20, "title change", "Edit live")

			sc.source.snapshot = nil
			sc.at(210, "end", tt.atEnd...)

			sc.source.recording = recording
			sc.at(215, "recording", tt.atRecording...)
			sc.at(216, "settled")

			sc.settled(tt.chat...)

			for _, text := range sc.chat.texts() {
				if kindOf(text) == "live" && !strings.Contains(text, "Day 3 - trains") {
					t.Errorf("live message misses the new title: %q", text)
				}
				if kindOf(text) == "offline" && !strings.Contains(text, recording.URL) {
					t.Errorf("offline message misses the recording: %q", text)
				}
			}
		})
	}
}

func TestLifecycleGraceExpires(t *testing.T) {
	sc := newScenario(t, domain.EndPolicyEditInPlace)

	sc.source.snapshot = snapshot("s1", "Day 3")
	sc.at(0, "go-live", "Send live", "Pin")

	sc.source.snapshot = nil
	sc.at(210, "end", "Unpin")
	sc.at(215, "waiting")
	sc.at(220, "grace over", "Edit offline")
	sc.at(221, "settled")

	sc.settled("offline")

	if texts := sc.chat.texts(); len(texts) != 1 || !strings.Contains(texts[0], "No recording available") {
		t.Errorf("chat texts = %q, want one offline message saying no recording is available", texts)
	}
}

func TestLifecycleStreamRestart(t *testing.T) {
	sc := newScenario(t, domain.EndPolicyEditInPlace)

	sc.source.snapshot = snapshot("s1", "Day 3")
	sc.at(0, "go-live", "Send live", "Pin")

	sc.source.snapshot = snapshot("s2", "Day 3, again")
	sc.at(30, "restart", "Unpin")
	sc.at(35, "waiting for the old recording")
	sc.at(40, "grace over", "Edit offline")
	sc.at(41, "old session closed")
	sc.at(42, "new go-live", "Send live", "Pin")

	if got, want := sc.chat.visible(), []string{"offline", "live pinned"}; !slices.Equal(got, want) {
		t.Errorf("chat = %q, want %q", got, want)
	}
}
