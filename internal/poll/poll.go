package poll

import (
	"context"
	"errors"
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
	GetDeliveries() []domain.Delivery
	SaveDelivery(domain.Delivery) error
}

type Config struct {
	ChatID       string
	Pin          bool
	EditOnChange bool
	OnEnd        domain.EndPolicy
	Style        string // Template style
	Lang         string // Template language
	EndGrace     time.Duration
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
		// still live with the same StreamID
		p.handleLive(ctx, session, snapshot)
	}
}

func (p *Poller) startSession(ctx context.Context, snapshot *domain.Snapshot) {
	slog.Info("poller: stream went live", "snapshot", snapshot)

	cfg := p.config

	session := &domain.Session{
		Channel:  p.channel,
		StreamID: snapshot.StreamID,
		State:    domain.SessionLive,
		Version:  1,
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
		return
	}

	d := domain.Delivery{
		StreamID:      snapshot.StreamID,
		Kind:          domain.DeliveryLive,
		State:         domain.DeliveryPublished,
		Ref:           session.LiveMessage,
		SyncedVersion: session.Version,
	}
	if err = p.store.SaveDelivery(d); err != nil {
		slog.Warn("poller: save delivery failed", "err", err, "delivery", d)
	}
}

func (p *Poller) handleLive(ctx context.Context, session *domain.Session, snapshot *domain.Snapshot) {
	if session.Title != snapshot.Title || session.Game != snapshot.Game {
		slog.Info("poller: stream info changed", "stream_id", session.StreamID, "title", snapshot.Title, "game", snapshot.Game)
		session.Title = snapshot.Title
		session.Game = snapshot.Game
		session.Version++

		if err := p.store.SetSession(*session); err != nil {
			slog.Warn("poller: set session failed", "err", err, "session", session)
			return
		}
	}

	if p.config.EditOnChange {
		p.syncLive(ctx, session)
	}
}

func (p *Poller) syncLive(ctx context.Context, session *domain.Session) {
	live := p.liveDelivery(session)

	if live.SyncedVersion == session.Version {
		return
	}

	streamEvent := message.StreamEvent{Session: *session, Timestamp: p.now().Unix()}

	text, err := message.FormatLive(p.config.Style, p.config.Lang, streamEvent)
	if err != nil {
		// formatting is deterministic, do not retry
		slog.Warn("poller: message formatting failed", "err", err, "stream_event", streamEvent)
	} else if err = p.sink.Edit(ctx, live.Ref, text); err != nil {
		slog.Warn("poller: message live edit failed", "err", err, "chat_id", live.Ref.ChatID)
		return
	}

	live.SyncedVersion = session.Version

	if err := p.store.SaveDelivery(live); err != nil {
		slog.Warn("poller: save delivery failed", "err", err, "kind", live.Kind)
	}
}

// liveDelivery returns the stored delivery of type Live
func (p *Poller) liveDelivery(session *domain.Session) domain.Delivery {
	for _, d := range p.store.GetDeliveries() {
		if d.Kind == domain.DeliveryLive {
			return d
		}
	}

	return domain.Delivery{
		StreamID: session.StreamID,
		Kind:     domain.DeliveryLive,
		State:    domain.DeliveryPublished,
		Ref:      session.LiveMessage,
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

	switch {
	case errors.Is(err, domain.ErrNoRecordings):
		slog.Debug("poller: source has no recordings, finalizing now", "platform", session.Platform)
	case err != nil:
		slog.Warn("poller: fetch recording failed", "err", err, "stream_id", session.StreamID)
		if !expired {
			return
		}
	case recording != nil:
		session.Recording = *recording
	case expired:
		slog.Info("poller: recording grace expired", "stream_id", session.StreamID)
	default:
		// keep waiting
		return
	}

	p.finalizeOrGiveUp(ctx, session, expired)
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
