package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/am-kenny/ampulsar/internal/client"
	"github.com/am-kenny/ampulsar/internal/config"
	"github.com/am-kenny/ampulsar/internal/domain"
	"github.com/am-kenny/ampulsar/internal/message"
)

func poll(ctx context.Context, tc *client.TwitchClient, tg *client.TelegramClient, tgChatID string, user *client.UserData, session *domain.Session, shouldPin bool, onEnd config.EndPolicy, templateStyle, templateLanguage string) *domain.Session {
	stream, err := tc.FetchStreamByUsername(ctx, user.Login)
	if err != nil {
		slog.Error("fetch stream failed", "err", err, "channel", user.Login)
		return session
	}

	switch {
	case stream != nil && session == nil:
		// WENT LIVE

		slog.Info("NOW LIVE")

		session = &domain.Session{
			Channel: domain.Channel{
				Platform:    domain.Twitch,
				ChannelID:   user.ID,
				Username:    user.Login,
				DisplayName: user.DisplayName,
			},
			StreamID: stream.ID,
			Title:    stream.Title,
			Game:     stream.GameName,
		}

		streamEvent := message.StreamEvent{Session: *session, Timestamp: time.Now().Unix()}

		text, err := message.FormatLive(templateStyle, templateLanguage, streamEvent)
		if err != nil {
			slog.Warn("message formatting failed", "err", err, "stream_event", streamEvent)
			return session
		}

		messageID, err := tg.SendHTMLMessage(ctx, tgChatID, text)
		if err != nil {
			slog.Warn("message send failed", "err", err, "tg_chat_id", tgChatID)
			return session
		}

		session.LiveMessageID = messageID

		if shouldPin {
			if err := tg.PinChatMessage(ctx, tgChatID, messageID); err != nil {
				slog.Warn("pin message failed", "err", err, "tg_chat_id", tgChatID)
			}
		}

	case stream == nil && session != nil:
		// WENT OFFLINE

		slog.Info("NOW OFFLINE")

		if shouldPin {
			if err := tg.UnpinChatMessage(ctx, tgChatID, session.LiveMessageID); err != nil {
				slog.Warn("unpin message failed", "err", err, "tg_chat_id", tgChatID)
			}
		}

		if onEnd == config.EndPolicyEditInPlace || onEnd == config.EndPolicyNewMessage {
			recording, err := tc.FetchStreamArchiveByUserIdAndStreamID(ctx, user.ID, session.StreamID)
			if err != nil {
				slog.Warn("fetch stream archive failed", "err", err, "stream_id", session.StreamID)
				return session
			}

			if recording != nil {
				session.Recording.RecordingURL = recording.URL
				session.Recording.Duration = recording.Duration
				session.Title = recording.Title
			} else {
				return session
			}
		}

		switch onEnd {
		case config.EndPolicyEditInPlace:
			{

				streamEvent := message.StreamEvent{Session: *session, Timestamp: time.Now().Unix()}
				text, err := message.FormatWentOffline(templateStyle, templateLanguage, streamEvent)
				if err != nil {
					slog.Warn("message formatting failed", "err", err, "stream_event", streamEvent)
					return session
				}

				if err := tg.EditHTMLMessageText(ctx, tgChatID, session.LiveMessageID, text); err != nil {
					slog.Warn("message edit failed", "err", err, "tg_chat_id", tgChatID)
					return session
				}
			}
		case config.EndPolicyNewMessage:
			{
				streamEvent := message.StreamEvent{Session: *session, Timestamp: time.Now().Unix()}
				text, err := message.FormatWentOffline(templateStyle, templateLanguage, streamEvent)
				if err != nil {
					slog.Warn("message formatting failed", "err", err, "stream_event", streamEvent)
					return session
				}

				_, err = tg.SendHTMLMessage(ctx, tgChatID, text)
				if err != nil {
					slog.Warn("message send failed", "err", err, "tg_chat_id", tgChatID)
					return session
				}
			}
		case config.EndPolicyDelete:
			{
				if err := tg.DeleteMessage(ctx, tgChatID, session.LiveMessageID); err != nil {
					slog.Warn("delete message failed", "err", err, "tg_chat_id", tgChatID)
					return session
				}
			}

		}

		session = nil

	default:
		// no transition — do nothing
	}
	return session
}

func main() {
	slog.Info("Starting AmPulsar")
	slog.Info("Loading config")

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}

	slog.Info("Configuration loaded successfully")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	twitchClient := client.NewTwitchClient(cfg.Twitch.ClientID, cfg.Twitch.ClientSecret)
	telegramClient := client.NewTelegramClient(cfg.Telegram.BotToken)

	user, err := twitchClient.FetchUserByUsername(ctx, cfg.Twitch.ChannelName)
	if err != nil {
		slog.Error("fetch twitch channel failed", "err", err, "channel", cfg.Twitch.ChannelName)
		os.Exit(1)
	}

	var session *domain.Session

	ticker := time.NewTicker(cfg.Poll.Interval)

	defer ticker.Stop()

	slog.Info("Starting poll", "channel", cfg.Twitch.ChannelName)

	session = poll(ctx, twitchClient, telegramClient, cfg.Telegram.ChatID, user, session, cfg.Telegram.Pin, cfg.Telegram.OnEnd, cfg.Template.Style, cfg.Template.Language)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Shutting down")
			return
		case <-ticker.C:
			session = poll(ctx, twitchClient, telegramClient, cfg.Telegram.ChatID, user, session, cfg.Telegram.Pin, cfg.Telegram.OnEnd, cfg.Template.Style, cfg.Template.Language)
		}
	}
}
