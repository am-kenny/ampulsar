package poll

import (
	"context"
	"log/slog"
	"time"

	"github.com/am-kenny/ampulsar/internal/domain"
	"github.com/am-kenny/ampulsar/internal/message"
)

type Source interface {
	FetchStream(ctx context.Context, channel domain.Channel) (*domain.Snapshot, error)
	FetchRecording(ctx context.Context, channel domain.Channel, streamID string) (*domain.Recording, error)
}

type Sink interface {
	Send(ctx context.Context, chatID, text string) (domain.MessageRef, error)
	Edit(ctx context.Context, ref domain.MessageRef, text string) error
	Delete(ctx context.Context, ref domain.MessageRef) error
	Pin(ctx context.Context, ref domain.MessageRef) error
	Unpin(ctx context.Context, ref domain.MessageRef) error
}

type Store interface {
	GetSession() *domain.Session
	SetSession(domain.Session) error
	DeleteSession() error
}

type Config struct {
	ChatID   string
	Pin      bool
	OnEnd    domain.EndPolicy
	Style    string // Template style
	Lang     string // Template language
	EndGrace time.Duration
}

type Poller struct {
	source  Source
	sink    Sink
	store   Store
	channel domain.Channel
	config  Config
	now     func() time.Time // function to retrieve current time
}

func NewPoller(source Source, sink Sink, store Store, channel domain.Channel, config Config, opts ...Option) *Poller {
	p := &Poller{
		source:  source,
		sink:    sink,
		store:   store,
		channel: channel,
		config:  config,
		now:     time.Now,
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

type Option func(*Poller)

func WithClock(now func() time.Time) Option {
	return func(p *Poller) { p.now = now }
}

func (p *Poller) Poll(ctx context.Context) {
	snapshot, err := p.source.FetchStream(ctx, p.channel)
	if err != nil {
		slog.Error("poller: fetch stream failed", "err", err, "channel", p.channel.Username)
		return
	}

	session := p.store.GetSession()

	if session != nil && session.State == "" {
		session.State = domain.SessionLive
	}

	switch {
	case session == nil && snapshot != nil:
		// went live
		p.startSession(ctx, snapshot)
	case session == nil:
		// offline and nothing to track
	case session.State == domain.SessionLive && snapshot == nil:
		// went offline
		p.markEnded(ctx, session)
		p.handleEnded(ctx, session)
	case session.State == domain.SessionLive && session.StreamID != snapshot.StreamID:
		// stream restarted between ticks
		p.markEnded(ctx, session)
		p.handleEnded(ctx, session)
	case session.State == domain.SessionEnded:
		// waiting for recording or retrying end-of-stream delivery
		p.handleEnded(ctx, session)
	default:
		// still live with the same StreamID, nothing to do
	}
}

func (p *Poller) startSession(ctx context.Context, snapshot *domain.Snapshot) {
	slog.Info("poller: stream went live", "snapshot", snapshot)

	cfg := p.config

	session := &domain.Session{
		Channel:  p.channel,
		StreamID: snapshot.StreamID,
		State:    domain.SessionLive,
		Title:    snapshot.Title,
		Game:     snapshot.Game,
		URL:      snapshot.URL,
	}

	streamEvent := message.StreamEvent{Session: *session, Timestamp: p.now().Unix()}

	text, err := message.FormatLive(cfg.Style, cfg.Lang, streamEvent)
	if err != nil {
		slog.Warn("poller: message formatting failed", "err", err, "stream_event", streamEvent)
		return
	}

	messageRef, err := p.sink.Send(ctx, cfg.ChatID, text)
	if err != nil {
		slog.Warn("poller: message send failed", "err", err, "chat_id", cfg.ChatID)
		return
	}

	session.LiveMessage = messageRef

	if cfg.Pin {
		if err := p.sink.Pin(ctx, session.LiveMessage); err != nil {
			slog.Warn("poller: pin message failed", "err", err, "chat_id", cfg.ChatID)
		}
	}

	if err := p.store.SetSession(*session); err != nil {
		slog.Warn("poller: set session failed", "err", err)
	}
}

// markEnded unpins the live message if configured and records the session as ended
func (p *Poller) markEnded(ctx context.Context, session *domain.Session) {
	slog.Info("poller: stream went offline")

	if p.config.Pin {
		if err := p.sink.Unpin(ctx, session.LiveMessage); err != nil {
			slog.Warn("poller: unpin message failed", "err", err, "chat_id", p.config.ChatID)
		}
	}

	session.State = domain.SessionEnded
	session.EndedAt = p.now()

	if err := p.store.SetSession(*session); err != nil {
		slog.Warn("poller: set session failed", "err", err)
	}
}

// handleEnded fetches recording if required by end policy and calls p.finalizeOrGiveUp
// this is the part that determines if grace threshold is reached
func (p *Poller) handleEnded(ctx context.Context, session *domain.Session) {
	slog.Debug("poller: ending session")

	expired := p.now().Sub(session.EndedAt) >= p.config.EndGrace

	if p.config.OnEnd == domain.EndPolicyDelete || p.config.OnEnd == domain.EndPolicyNone {
		p.finalizeOrGiveUp(ctx, session, expired)
		return
	}

	recording, err := p.source.FetchRecording(ctx, p.channel, session.StreamID)
	if err != nil {
		slog.Warn("poller: fetch recording failed", "err", err, "stream_id", session.StreamID)
	}

	switch {
	case recording != nil:
		session.Recording = *recording
		p.finalizeOrGiveUp(ctx, session, expired)
	case expired:
		slog.Info("poller: recording grace expired", "stream_id", session.StreamID)
		p.closeSession()
	default:
		// keep waiting
	}
}

// finalizeOrGiveUp calls finalize; calls closeSession if call succeeds or if grace period is expired
func (p *Poller) finalizeOrGiveUp(ctx context.Context, session *domain.Session, expired bool) {
	if err := p.finalize(ctx, session); err != nil {
		slog.Warn("poller: finalize failed", "err", err)
		if !expired {
			return
		}
	}

	p.closeSession()
}

// finalize puts end policy into action
func (p *Poller) finalize(ctx context.Context, session *domain.Session) error {
	cfg := p.config

	switch cfg.OnEnd {
	case domain.EndPolicyEditInPlace, domain.EndPolicyNewMessage:
		streamEvent := message.StreamEvent{Session: *session, Timestamp: p.now().Unix()}
		text, err := message.FormatWentOffline(cfg.Style, cfg.Lang, streamEvent)
		if err != nil {
			// formatting is deterministic, do not retry
			slog.Warn("poller: message formatting failed", "err", err, "stream_event", streamEvent)
			return nil
		}

		if cfg.OnEnd == domain.EndPolicyEditInPlace {
			return p.sink.Edit(ctx, session.LiveMessage, text)
		}

		_, err = p.sink.Send(ctx, cfg.ChatID, text)
		return err

	case domain.EndPolicyDelete:
		return p.sink.Delete(ctx, session.LiveMessage)
	case domain.EndPolicyNone:
		// No extra actions
	}

	return nil
}

func (p *Poller) closeSession() {
	if err := p.store.DeleteSession(); err != nil {
		slog.Warn("poller: delete session failed", "err", err)
	}
}
