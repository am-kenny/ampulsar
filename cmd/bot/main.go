package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/adrg/xdg"

	"github.com/am-kenny/ampulsar/internal/config"
	"github.com/am-kenny/ampulsar/internal/poll"
	"github.com/am-kenny/ampulsar/internal/store"
	"github.com/am-kenny/ampulsar/internal/telegram"
	"github.com/am-kenny/ampulsar/internal/twitch"
)

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

	twitchClient := twitch.NewClient(cfg.Twitch.ClientID, cfg.Twitch.ClientSecret)
	twitchSource := twitch.NewSource(twitchClient)

	telegramClient := telegram.NewClient(cfg.Telegram.BotToken)
	telegramSink := telegram.NewSink(telegramClient)

	channel, err := twitchSource.ResolveChannel(ctx, cfg.Twitch.ChannelName)
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

	pollCfg := poll.Config{
		ChatID: cfg.Telegram.ChatID,
		Pin:    cfg.Telegram.Pin,
		OnEnd:  cfg.Telegram.OnEnd,
		Style:  cfg.Template.Style,
		Lang:   cfg.Template.Language,
	}
	poller := poll.NewPoller(twitchSource, telegramSink, st, *channel, pollCfg)

	slog.Info("Starting poll", "channel", channel.Username)
	poller.Poll(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Shutting down")
			return
		case <-ticker.C:
			poller.Poll(ctx)
		}
	}
}
