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

func (s *Source) ResolveChannel(ctx context.Context, username string) (domain.Channel, error) {
	ch, err := s.client.FetchUserByUsername(ctx, username)
	if err != nil {
		return domain.Channel{}, err
	}

	return domain.Channel{
		Platform:    domain.Twitch,
		ID:          ch.ID,
		Username:    ch.Login,
		DisplayName: ch.DisplayName,
	}, nil
}

func (s *Source) FetchStream(ctx context.Context, ch domain.Channel) (*domain.Snapshot, error) {
	stream, err := s.client.FetchStreamByUsername(ctx, ch.Username)
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
		URL:       "https://www.twitch.tv/" + ch.Username,
	}, nil
}

func (s *Source) FetchRecording(ctx context.Context, ch domain.Channel, streamID string) (*domain.Recording, error) {
	recording, err := s.client.FetchStreamArchiveByUserIdAndStreamID(ctx, ch.ID, streamID)
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
