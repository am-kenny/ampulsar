package twitch

import (
	"context"
	"fmt"
	"time"

	"github.com/am-kenny/ampulsar/internal/domain"
)

type Source struct {
	client *Client
}

func NewSource(client *Client) *Source {
	return &Source{client: client}
}

func (s *Source) ResolveChannel(ctx context.Context, username string) (*domain.Channel, error) {
	channel, err := s.client.FetchUserByUsername(ctx, username)
	if err != nil {
		return nil, err
	}

	return &domain.Channel{
		Platform:    domain.Twitch,
		ID:          channel.ID,
		Username:    channel.Login,
		DisplayName: channel.DisplayName,
	}, nil
}

func (s *Source) FetchStream(ctx context.Context, username string) (*domain.Snapshot, error) {
	stream, err := s.client.FetchStreamByUsername(ctx, username)
	if err != nil {
		return nil, err
	}

	if stream == nil {
		return nil, nil
	}

	startTime, err := time.Parse(time.RFC3339, stream.StartedAt)
	if err != nil {
		return nil, fmt.Errorf("source: failed to parse stream start time %s: %w", stream.StartedAt, err)
	}

	return &domain.Snapshot{
		StreamID:  stream.ID,
		Title:     stream.Title,
		Game:      stream.GameName,
		StartedAt: startTime,
	}, nil
}

func (s *Source) FetchRecording(ctx context.Context, channelID, streamID string) (*domain.Recording, error) {
	recording, err := s.client.FetchStreamArchiveByUserIdAndStreamID(ctx, channelID, streamID)
	if err != nil {
		return nil, err
	}

	if recording == nil {
		return nil, nil
	}

	var duration time.Duration
	if recording.Duration != "" {
		duration, err = time.ParseDuration(recording.Duration)
		if err != nil {
			return nil, fmt.Errorf("source: failed to parse recording duration %s: %w", recording.Duration, err)
		}
	}

	return &domain.Recording{
		URL:      recording.URL,
		Title:    recording.Title,
		Duration: duration,
	}, nil
}
