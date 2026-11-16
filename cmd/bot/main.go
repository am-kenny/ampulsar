package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/adrg/xdg"

	"github.com/am-kenny/ampulsar/internal/config"
	"github.com/am-kenny/ampulsar/internal/health"
	"github.com/am-kenny/ampulsar/internal/message"
	"github.com/am-kenny/ampulsar/internal/poll"
	"github.com/am-kenny/ampulsar/internal/reconcile"
	"github.com/am-kenny/ampulsar/internal/store"
	"github.com/am-kenny/ampulsar/internal/telegram"
	"github.com/am-kenny/ampulsar/internal/tiktok"
	"github.com/am-kenny/ampulsar/internal/twitch"
)

var version = "dev"

func main() {
	slog.Info("Starting AmPulsar", "version", version)
	slog.Info("Loading config")

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}

	slog.Info("Configuration loaded successfully")

	if err = message.Check(cfg.Template.Style, cfg.Template.Language); err != nil {
		slog.Error("template check failed", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	source, channelName, err := buildSource(cfg)
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

	reconciler := reconcile.NewReconciler(telegramSink, st, reconcile.Config{
		ChatID:       cfg.Telegram.ChatID,
		Pin:          cfg.Telegram.Pin,
		EditOnChange: cfg.Telegram.EditOnChange,
		OnEnd:        cfg.Telegram.OnEnd,
		Style:        cfg.Template.Style,
		Lang:         cfg.Template.Language,
		Interval:     cfg.Reconcile.Interval,
	})

	pollCfg := poll.Config{
		Interval:         cfg.Poll.Interval,
		EndGrace:         cfg.Poll.EndGrace,
		WaitForRecording: cfg.Telegram.OnEnd.NeedsRecording(),
	}

	poller := poll.NewPoller(source, st, channelName, pollCfg,
		poll.WithKick(reconciler.Kick),
	)

	healthServer, err := health.Listen(cfg.Health.Addr,
		health.Check{Name: "poller", Last: poller.LastPoll, MaxAge: maxAge(cfg.Poll.Interval)},
		health.Check{Name: "reconciler", Last: reconciler.LastSync, MaxAge: maxAge(cfg.Reconcile.Interval)},
	)
	if err != nil {
		slog.Error("health server init failed", "err", err, "addr", cfg.Health.Addr)
		os.Exit(1)
	}
	slog.Info("Health server listening", "addr", healthServer.Addr())

	slog.Info("Starting poll and reconcile", "channel", channelName)

	var wg sync.WaitGroup
	wg.Go(func() { healthServer.Run(ctx) })
	wg.Go(func() { reconciler.Run(ctx) })
	wg.Go(func() { poller.Run(ctx) })
	wg.Wait()

	slog.Info("Shutting down")
}

// buildSource returns the configured source and channel name.
func buildSource(cfg *config.Config) (poll.Source, string, error) {
	switch {
	case cfg.Twitch.Enabled:
		return twitch.NewSource(twitch.NewClient(cfg.Twitch.ClientID, cfg.Twitch.ClientSecret)), cfg.Twitch.ChannelName, nil

	case cfg.TikTok.Enabled:
		ua := tiktok.WithUserAgent(tiktok.UserAgent(version))
		return tiktok.NewSource(tiktok.NewClient(ua)), cfg.TikTok.Username, nil

	default:
		return nil, "", errors.New("no source platform configured")
	}
}

// maxAge returns how long a loop may go without finishing before it counts as stuck
func maxAge(interval time.Duration) time.Duration {
	return 3*interval + 5*time.Minute
}
