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
	"github.com/am-kenny/ampulsar/internal/message"
)

type streamState struct {
	isLive    bool
	streamID  string
	messageID int
}

func poll(ctx context.Context, tc *client.TwitchClient, tg *client.TelegramClient, tgChatID string, user *client.UserData, state *streamState, shouldPin bool, onEnd config.EndPolicy, templateStyle, templateLanguage string) {
	stream, err := tc.FetchStreamByUsername(ctx, user.Login)
	if err != nil {
		slog.Error("fetch stream failed", "err", err, "channel", user.Login)
		return
	}

	streamEvent := message.StreamEvent{
		DisplayName: user.DisplayName,
		Username:    user.Login,

		Timestamp: time.Now().Unix(),
	}

	switch {
	case stream != nil && !state.isLive:
		// WENT LIVE

		slog.Info("NOW LIVE")

		streamEvent.Title = stream.Title
		streamEvent.Game = stream.GameName

		text, err := message.FormatLive(templateStyle, templateLanguage, streamEvent)
		if err != nil {
			slog.Warn("message formatting failed", "err", err, "streamEvent", streamEvent)
			return
		}

		messageID, err := tg.SendHTMLMessage(ctx, tgChatID, text)
		if err != nil {
			slog.Warn("message send failed", "err", err, "tgChatID", tgChatID)
			return
		}

		state.isLive = true
		state.streamID = stream.ID
		state.messageID = messageID

		if shouldPin {
			if err := tg.PinChatMessage(ctx, tgChatID, messageID); err != nil {
				slog.Warn("pin message failed", "err", err, "tgChatID", tgChatID)
			}
		}

	case stream == nil && state.isLive:
		// WENT OFFLINE

		slog.Info("NOW OFFLINE")

		if shouldPin {
			if err := tg.UnpinChatMessage(ctx, tgChatID, state.messageID); err != nil {
				slog.Warn("unpin message failed", "err", err, "tgChatID", tgChatID)
			}
		}

		if onEnd == config.EndPolicyEditInPlace || onEnd == config.EndPolicyNewMessage {
			recording, err := tc.FetchStreamArchiveByUserIdAndStreamID(ctx, user.ID, state.streamID)
			if err != nil {
				slog.Warn("fetch stream archive failed", "err", err, "streamID", state.streamID)
				return
			}

			if recording != nil {
				streamEvent.RecordingURL = recording.URL
				streamEvent.Duration = recording.Duration
				streamEvent.Title = recording.Title
			} else {
				return
			}
		}

		switch onEnd {
		case config.EndPolicyEditInPlace:
			{

				text, err := message.FormatWentOffline(templateStyle, templateLanguage, streamEvent)
				if err != nil {
					slog.Warn("message formatting failed", "err", err, "streamEvent", streamEvent)
					return
				}

				if err := tg.EditHTMLMessageText(ctx, tgChatID, state.messageID, text); err != nil {
					slog.Warn("message edit failed", "err", err, "tgChatID", tgChatID)
					return
				}
			}
		case config.EndPolicyNewMessage:
			{
				text, err := message.FormatWentOffline(templateStyle, templateLanguage, streamEvent)
				if err != nil {
					slog.Warn("message formatting failed", "err", err, "streamEvent", streamEvent)
					return
				}

				_, err = tg.SendHTMLMessage(ctx, tgChatID, text)
				if err != nil {
					slog.Warn("message send failed", "err", err, "tgChatID", tgChatID)
					return
				}
			}
		case config.EndPolicyDelete:
			{
				if err := tg.DeleteMessage(ctx, tgChatID, state.messageID); err != nil {
					slog.Warn("delete message failed", "err", err, "tgChatID", tgChatID)
					return
				}
			}

		}

		state.isLive = false
		state.streamID = ""
		state.messageID = 0

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

	state := streamState{}

	ticker := time.NewTicker(cfg.Poll.Interval)

	defer ticker.Stop()

	slog.Info("Starting poll", "channel", cfg.Twitch.ChannelName)

	poll(ctx, twitchClient, telegramClient, cfg.Telegram.ChatID, user, &state, cfg.Telegram.Pin, cfg.Telegram.OnEnd, cfg.Template.Style, cfg.Template.Language)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Shutting down")
			return
		case <-ticker.C:
			poll(ctx, twitchClient, telegramClient, cfg.Telegram.ChatID, user, &state, cfg.Telegram.Pin, cfg.Telegram.OnEnd, cfg.Template.Style, cfg.Template.Language)
		}
	}

}
