package poll

import (
	"context"
	"log/slog"
	"time"

	"github.com/am-kenny/ampulsar/internal/domain"
	"github.com/am-kenny/ampulsar/internal/message"
)

type Source interface {
	FetchStream(ctx context.Context, username string) (*domain.Snapshot, error)
	FetchRecording(ctx context.Context, channelID, streamID string) (*domain.Recording, error)
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
	ChatID         string
	Pin            bool
	OnEnd          domain.EndPolicy
	Style          string // Template style
	Lang           string // Template language
	RecordingGrace time.Duration
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
	snapshot, err := p.source.FetchStream(ctx, p.channel.Username)
	if err != nil {
		slog.Error("fetch stream failed", "err", err, "channel", p.channel.Username)
		return
	}

	session := p.store.GetSession()

	switch {
	case session == nil && snapshot != nil:
		// went live
		p.processOnline(ctx, snapshot)
	case session == nil:
		// offline and nothing to track
	case session.State == domain.SessionLive && snapshot == nil:
		// went offline
		p.processOffline(ctx, session)
	case session.State == domain.SessionLive && session.StreamID != snapshot.StreamID:
		// stream restarted between ticks
		p.processOffline(ctx, session)
	case session.State == domain.SessionEnded:
		p.processEnded(ctx, session)
	}
}

func (p *Poller) processOnline(ctx context.Context, snapshot *domain.Snapshot) {
	slog.Info("NOW LIVE")

	cfg := p.config

	session := &domain.Session{
		Channel:  p.channel,
		StreamID: snapshot.StreamID,
		State:    domain.SessionLive,
		Title:    snapshot.Title,
		Game:     snapshot.Game,
	}

	streamEvent := message.StreamEvent{Session: *session, Timestamp: time.Now().Unix()}

	text, err := message.FormatLive(cfg.Style, cfg.Lang, streamEvent)
	if err != nil {
		slog.Warn("message formatting failed", "err", err, "stream_event", streamEvent)
		return
	}

	messageRef, err := p.sink.Send(ctx, cfg.ChatID, text)
	if err != nil {
		slog.Warn("message send failed", "err", err, "chat_id", cfg.ChatID)
		return
	}

	session.LiveMessage = messageRef

	if cfg.Pin {
		if err := p.sink.Pin(ctx, session.LiveMessage); err != nil {
			slog.Warn("pin message failed", "err", err, "chat_id", cfg.ChatID)
		}
	}

	if err := p.store.SetSession(*session); err != nil {
		slog.Warn("set session failed", "err", err)
	}
}

func (p *Poller) processOffline(ctx context.Context, session *domain.Session) {
	slog.Info("NOW OFFLINE")

	if p.config.Pin {
		if err := p.sink.Unpin(ctx, session.LiveMessage); err != nil {
			slog.Warn("unpin message failed", "err", err, "chat_id", p.config.ChatID)
		}
	}

	session.State = domain.SessionEnded
	session.EndedAt = p.now()

	if err := p.store.SetSession(*session); err != nil {
		slog.Warn("set session failed", "err", err)
	}

	p.processEnded(ctx, session)
}

func (p *Poller) processEnded(ctx context.Context, session *domain.Session) {
	slog.Info("poller: ending session")

	cfg := p.config

	if cfg.OnEnd == domain.EndPolicyDelete || cfg.OnEnd == domain.EndPolicyNone {
		p.finalize(ctx, session)
		return
	}

	recording, err := p.source.FetchRecording(ctx, p.channel.ID, session.StreamID)
	if err != nil {
		slog.Warn("fetch recording failed", "err", err, "stream_id", session.StreamID)
	}

	switch {
	case recording != nil:
		session.Recording = *recording
		p.finalize(ctx, session)
	case p.now().Sub(session.EndedAt) >= cfg.RecordingGrace:
		slog.Info("recording grace expired", "stream_id", session.StreamID)
		p.closeSession()
	default:
		// keep waiting
	}

}

func (p *Poller) finalize(ctx context.Context, session *domain.Session) {
	cfg := p.config

	switch cfg.OnEnd {
	case domain.EndPolicyEditInPlace, domain.EndPolicyNewMessage:
		streamEvent := message.StreamEvent{Session: *session, Timestamp: time.Now().Unix()}
		text, err := message.FormatWentOffline(cfg.Style, cfg.Lang, streamEvent)
		if err != nil {
			slog.Warn("message formatting failed", "err", err, "stream_event", streamEvent)
			break
		}

		if cfg.OnEnd == domain.EndPolicyEditInPlace {
			if err := p.sink.Edit(ctx, session.LiveMessage, text); err != nil {
				slog.Warn("message edit failed", "err", err, "chat_id", cfg.ChatID)
				return
			}
		}

		if cfg.OnEnd == domain.EndPolicyNewMessage {
			_, err := p.sink.Send(ctx, cfg.ChatID, text)
			if err != nil {
				slog.Warn("message send failed", "err", err, "chat_id", cfg.ChatID)
				return
			}
		}

	case domain.EndPolicyDelete:
		if err := p.sink.Delete(ctx, session.LiveMessage); err != nil {
			slog.Warn("delete message failed", "err", err, "chat_id", cfg.ChatID)
			return
		}
	case domain.EndPolicyNone:
		// No extra actions
	}

	p.closeSession()
}

func (p *Poller) closeSession() {
	if err := p.store.DeleteSession(); err != nil {
		slog.Warn("delete session failed", "err", err)
	}
}
