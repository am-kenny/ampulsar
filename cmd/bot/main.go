package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/adrg/xdg"

	"github.com/am-kenny/ampulsar/internal/client"
	"github.com/am-kenny/ampulsar/internal/config"
	"github.com/am-kenny/ampulsar/internal/domain"
	"github.com/am-kenny/ampulsar/internal/message"
	"github.com/am-kenny/ampulsar/internal/store"
)

func poll(ctx context.Context, tc *client.TwitchClient, tg *client.TelegramClient, tgChatID string, user *client.UserData, sessionStore *store.Store, shouldPin bool, onEnd domain.EndPolicy, templateStyle, templateLanguage string) {
	stream, err := tc.FetchStreamByUsername(ctx, user.Login)
	if err != nil {
		slog.Error("fetch stream failed", "err", err, "channel", user.Login)
		return
	}

	session := sessionStore.GetSession()

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
			return
		}

		messageID, err := tg.SendHTMLMessage(ctx, tgChatID, text)
		if err != nil {
			slog.Warn("message send failed", "err", err, "tg_chat_id", tgChatID)
			return
		}

		session.LiveMessageID = messageID

		if shouldPin {
			if err := tg.PinChatMessage(ctx, tgChatID, messageID); err != nil {
				slog.Warn("pin message failed", "err", err, "tg_chat_id", tgChatID)
			}
		}

		if err = sessionStore.SetSession(*session); err != nil {
			slog.Warn("set session failed", "err", err)
		}

	case stream == nil && session != nil:
		// WENT OFFLINE

		slog.Info("NOW OFFLINE")

		if shouldPin {
			if err := tg.UnpinChatMessage(ctx, tgChatID, session.LiveMessageID); err != nil {
				slog.Warn("unpin message failed", "err", err, "tg_chat_id", tgChatID)
			}
		}

		if onEnd == domain.EndPolicyEditInPlace || onEnd == domain.EndPolicyNewMessage {
			recording, err := tc.FetchStreamArchiveByUserIdAndStreamID(ctx, user.ID, session.StreamID)
			if err != nil {
				slog.Warn("fetch stream archive failed", "err", err, "stream_id", session.StreamID)
				return
			}

			if recording != nil {
				session.Recording.RecordingURL = recording.URL
				session.Recording.Duration = recording.Duration
				session.Title = recording.Title
			} else {
				return
			}
		}

		switch onEnd {
		case domain.EndPolicyEditInPlace:
			{

				streamEvent := message.StreamEvent{Session: *session, Timestamp: time.Now().Unix()}
				text, err := message.FormatWentOffline(templateStyle, templateLanguage, streamEvent)
				if err != nil {
					slog.Warn("message formatting failed", "err", err, "stream_event", streamEvent)
					return
				}

				if err := tg.EditHTMLMessageText(ctx, tgChatID, session.LiveMessageID, text); err != nil {
					slog.Warn("message edit failed", "err", err, "tg_chat_id", tgChatID)
					return
				}
			}
		case domain.EndPolicyNewMessage:
			{
				streamEvent := message.StreamEvent{Session: *session, Timestamp: time.Now().Unix()}
				text, err := message.FormatWentOffline(templateStyle, templateLanguage, streamEvent)
				if err != nil {
					slog.Warn("message formatting failed", "err", err, "stream_event", streamEvent)
					return
				}

				_, err = tg.SendHTMLMessage(ctx, tgChatID, text)
				if err != nil {
					slog.Warn("message send failed", "err", err, "tg_chat_id", tgChatID)
					return
				}
			}
		case domain.EndPolicyDelete:
			{
				if err := tg.DeleteMessage(ctx, tgChatID, session.LiveMessageID); err != nil {
					slog.Warn("delete message failed", "err", err, "tg_chat_id", tgChatID)
					return
				}
			}

		}

		if err = sessionStore.DeleteSession(); err != nil {
			slog.Warn("delete session failed", "err", err)
		}

	default:
		// no transition — do nothing
	}
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

	storePath := cfg.Store.Path
	if storePath == "" {
		storePath, err = xdg.StateFile("ampulsar/session.json")
		if err != nil {
			slog.Error("state directory init failed", "err", err, "path", storePath)
			os.Exit(1)
		}
	}

	st, err := store.NewFile(storePath)
	if err != nil {
		slog.Error("file store init failed", "err", err, "path", storePath)
		os.Exit(1)
	}
	slog.Info("File store loaded successfully", "path", storePath, "has_session", st.GetSession() != nil)

	ticker := time.NewTicker(cfg.Poll.Interval)

	defer ticker.Stop()

	slog.Info("Starting poll", "channel", cfg.Twitch.ChannelName)

	poll(ctx, twitchClient, telegramClient, cfg.Telegram.ChatID, user, st, cfg.Telegram.Pin, cfg.Telegram.OnEnd, cfg.Template.Style, cfg.Template.Language)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Shutting down")
			return
		case <-ticker.C:
			poll(ctx, twitchClient, telegramClient, cfg.Telegram.ChatID, user, st, cfg.Telegram.Pin, cfg.Telegram.OnEnd, cfg.Template.Style, cfg.Template.Language)
		}
	}
}
