package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/adrg/xdg"

	"github.com/am-kenny/ampulsar/internal/config"
	"github.com/am-kenny/ampulsar/internal/domain"
	"github.com/am-kenny/ampulsar/internal/poll"
	"github.com/am-kenny/ampulsar/internal/store"
	"github.com/am-kenny/ampulsar/internal/telegram"
	"github.com/am-kenny/ampulsar/internal/tiktok"
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

	source, channel, err := buildSource(ctx, cfg)
	if err != nil {
		slog.Error("build source failed", "err", err)
		os.Exit(1)
	}

	telegramClient := telegram.NewClient(cfg.Telegram.BotToken)
	telegramSink := telegram.NewSink(telegramClient)

	storePath := cfg.Store.Path
	if storePath == "" {
		storePath, err = xdg.StateFile("ampulsar/state.json")
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
		ChatID:       cfg.Telegram.ChatID,
		Pin:          cfg.Telegram.Pin,
		EditOnChange: cfg.Telegram.EditOnChange,
		OnEnd:        cfg.Telegram.OnEnd,
		Style:        cfg.Template.Style,
		Lang:         cfg.Template.Language,
		EndGrace:     cfg.Poll.EndGrace,
	}
	poller := poll.NewPoller(source, telegramSink, st, channel, pollCfg)

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

func buildSource(ctx context.Context, cfg *config.Config) (poll.Source, domain.Channel, error) {
	switch {
	case cfg.Twitch.Active():
		s := twitch.NewSource(twitch.NewClient(cfg.Twitch.ClientID, cfg.Twitch.ClientSecret))
		ch, err := s.ResolveChannel(ctx, cfg.Twitch.ChannelName)
		if err != nil {
			return nil, domain.Channel{}, fmt.Errorf("resolve twitch channel %q: %w", cfg.Twitch.ChannelName, err)
		}
		return s, ch, nil

	case cfg.TikTok.Active():
		s := tiktok.NewSource(tiktok.NewClient())
		ch, err := s.ResolveChannel(ctx, cfg.TikTok.Username)
		if err != nil {
			return nil, domain.Channel{}, fmt.Errorf("resolve tiktok channel %q: %w", cfg.TikTok.Username, err)
		}
		return s, ch, nil

	default:
		return nil, domain.Channel{}, errors.New("no source platform configured")
	}
}
