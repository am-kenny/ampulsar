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
	ChatID string
	Pin    bool
	OnEnd  domain.EndPolicy
	Style  string // Template style
	Lang   string // Template language
}

type Poller struct {
	source  Source
	sink    Sink
	store   Store
	channel domain.Channel
	config  Config
}

func NewPoller(source Source, sink Sink, store Store, channel domain.Channel, config Config) *Poller {
	return &Poller{
		source:  source,
		sink:    sink,
		store:   store,
		channel: channel,
		config:  config,
	}
}

func (p *Poller) Poll(ctx context.Context) {
	cfg := p.config

	snapshot, err := p.source.FetchStream(ctx, p.channel.Username)
	if err != nil {
		slog.Error("fetch stream failed", "err", err, "channel", p.channel.Username)
		return
	}

	session := p.store.GetSession()

	switch {
	case snapshot != nil && session == nil:
		// WENT LIVE
		slog.Info("NOW LIVE")

		session = &domain.Session{
			Channel:  p.channel,
			StreamID: snapshot.StreamID,
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

		if err = p.store.SetSession(*session); err != nil {
			slog.Warn("set session failed", "err", err)
		}

	case snapshot == nil && session != nil:
		// WENT OFFLINE
		slog.Info("NOW OFFLINE")

		if cfg.Pin {
			if err := p.sink.Unpin(ctx, session.LiveMessage); err != nil {
				slog.Warn("unpin message failed", "err", err, "chat_id", cfg.ChatID)
			}
		}

		var offlineText string
		if cfg.OnEnd == domain.EndPolicyEditInPlace || cfg.OnEnd == domain.EndPolicyNewMessage {
			recording, err := p.source.FetchRecording(ctx, p.channel.ID, session.StreamID)
			if err != nil {
				slog.Warn("fetch stream archive failed", "err", err, "stream_id", session.StreamID)
				return
			}

			if recording == nil {
				return
			}
			session.Recording = *recording

			streamEvent := message.StreamEvent{Session: *session, Timestamp: time.Now().Unix()}
			offlineText, err = message.FormatWentOffline(cfg.Style, cfg.Lang, streamEvent)
			if err != nil {
				slog.Warn("message formatting failed", "err", err, "stream_event", streamEvent)
				return
			}
		}

		switch cfg.OnEnd {
		case domain.EndPolicyEditInPlace:
			{
				if err := p.sink.Edit(ctx, session.LiveMessage, offlineText); err != nil {
					slog.Warn("message edit failed", "err", err, "chat_id", cfg.ChatID)
					return
				}
			}
		case domain.EndPolicyNewMessage:
			{
				_, err = p.sink.Send(ctx, cfg.ChatID, offlineText)
				if err != nil {
					slog.Warn("message send failed", "err", err, "chat_id", cfg.ChatID)
					return
				}
			}
		case domain.EndPolicyDelete:
			{
				if err := p.sink.Delete(ctx, session.LiveMessage); err != nil {
					slog.Warn("delete message failed", "err", err, "chat_id", cfg.ChatID)
					return
				}
			}

		}

		if err = p.store.DeleteSession(); err != nil {
			slog.Warn("delete session failed", "err", err)
		}

	default:
		// no transition — do nothing
	}
}
