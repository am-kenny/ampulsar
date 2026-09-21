package tiktok

import (
	"context"
	"time"

	"github.com/am-kenny/ampulsar/internal/domain"
)

// Source maps TikTok responses to domain types
type Source struct {
	client *Client
}

func NewSource(c *Client) *Source {
	return &Source{client: c}
}

func (s *Source) Platform() domain.Platform {
	return domain.TikTok
}

func (s *Source) ResolveChannel(ctx context.Context, username string) (domain.Channel, error) {
	user, err := s.client.FetchUserByUsername(ctx, username)
	if err != nil {
		return domain.Channel{}, err
	}

	return domain.Channel{Platform: domain.TikTok, ID: user.ID, Username: user.UniqueID, DisplayName: user.Nickname}, nil
}

// FetchStream returns nil if the channel is offline
func (s *Source) FetchStream(ctx context.Context, ch domain.Channel) (*domain.Snapshot, error) {
	room, err := s.client.FetchUserRoomByUsername(ctx, ch.Username)
	if err != nil {
		return nil, err
	}

	if room == nil {
		return nil, nil
	}

	var startedAt time.Time
	if room.LiveRoom.StartTime > 0 {
		startedAt = time.Unix(room.LiveRoom.StartTime, 0).UTC()
	}

	return &domain.Snapshot{
		StreamID:  room.User.RoomID,
		Title:     room.LiveRoom.Title,
		StartedAt: startedAt,
	}, nil
}

// FetchRecording always returns nil: TikTok LIVE replays are not public
func (s *Source) FetchRecording(ctx context.Context, ch domain.Channel, streamID string) (*domain.Recording, error) {
	return nil, nil
}
