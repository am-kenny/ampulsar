package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
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

func poll(ctx context.Context, tc *client.TwitchClient, tg *client.TelegramClient, tgChatID string, user *client.UserData, state *streamState) {
	stream, err := tc.FetchStreamByUsername(ctx, user.Login)
	if err != nil {
		// log and continue — a transient API error shouldn't crash the loop
		return
	}

	streamEvent := message.StreamEvent{
		DisplayName: user.DisplayName,
		Username:    user.Login,
	}

	switch {
	case stream != nil && !state.isLive:
		// WENT LIVE
		// build message.StreamEvent from stream + userID
		// message.FormatLive(...)
		// tg.SendMessage(...) -> capture messageID
		// update state: isLive=true, streamID=stream.ID, messageID=...

		fmt.Println("NOW LIVE")

		streamEvent.Title = stream.Title
		streamEvent.Game = stream.GameName

		text, err := message.FormatLive("default", "ru", streamEvent)
		if err != nil {
			log.Println(err)
			return
		}

		messageID, err := tg.SendHTMLMessage(ctx, tgChatID, text)
		if err != nil {
			log.Println(err)
			return
		}

		state.isLive = true
		state.streamID = stream.ID
		state.messageID = messageID

		if err := tg.PinChatMessage(ctx, tgChatID, strconv.Itoa(messageID)); err != nil {
			log.Println(err)
		}

	case stream == nil && state.isLive:
		// WENT OFFLINE
		// tc.FetchStreamArchiveByUserIdAndStreamID(ctx, userID, state.streamID)
		// build message.StreamEvent (offline), formatted with or without RecordingURL
		// tg.SendMessage(...)
		// reset state: isLive=false, streamID="", messageID=0

		fmt.Println("NOW OFFLINE")

		recording, err := tc.FetchStreamArchiveByUserIdAndStreamID(ctx, user.ID, state.streamID)
		if err != nil {
			log.Println(err)
			return
		}

		if recording != nil {
			streamEvent.RecordingURL = recording.URL
			streamEvent.Duration = recording.Duration
			streamEvent.Title = recording.Title
		} else {
			return
		}

		text, err := message.FormatWentOffline("default", "ru", streamEvent)
		if err != nil {
			log.Println(err)
			return
		}

		if err := tg.EditHTMLMessageText(ctx, tgChatID, strconv.Itoa(state.messageID), text); err != nil {
			log.Println(err)
			return
		}

		state.isLive = false
		state.streamID = ""
		state.messageID = 0

	default:
		// no transition — do nothing
		fmt.Println("-")
	}
}

func main() {
	// 7. loop:
	//      select {
	//      case <-ctx.Done(): return
	//      case <-ticker.C:
	//          poll(ctx, twitchClient, telegramClient, cfg, &state)
	//      }
	fmt.Println("Starting AmPulsar")
	fmt.Println("Loading config")

	cfg, err := config.Load()
	if err != nil {
		log.Fatal(fmt.Errorf("Application misconfigured: %s", err))
	} else {
		fmt.Println("Configuration loaded successfully")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	twitchClient := client.NewTwitchClient(cfg.Twitch.ClientID, cfg.Twitch.ClientSecret)
	telegramClient := client.NewTelegramClient(cfg.Telegram.BotToken)

	user, err := twitchClient.FetchUserByUsername(ctx, cfg.Twitch.ChannelName)
	if err != nil {
		log.Fatal(err)
	}

	state := streamState{}

	ticker := time.NewTicker(5 * time.Minute)

	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("Shutting down")
			return
		case <-ticker.C:
			poll(ctx, twitchClient, telegramClient, cfg.Telegram.ChatID, user, &state)
		}
	}

}
