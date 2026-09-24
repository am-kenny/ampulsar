package tiktok

import (
	"context"
	"errors"
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
	profile, err := s.client.FetchProfileByUsername(ctx, username)
	if err != nil {
		return domain.Channel{}, err
	}

	ch := domain.Channel{Platform: domain.TikTok, Username: profile.EmbedProductID, DisplayName: profile.AuthorName}
	user, err := s.client.FetchUserByUsername(ctx, ch.Username)
	switch {
	case err == nil:
		ch.ID = user.ID
	case errors.Is(err, ErrNoLiveRoom):
		// Never gone live
	default:
		return domain.Channel{}, err
	}

	return ch, nil
}

// FetchStream returns nil if the channel is offline
func (s *Source) FetchStream(ctx context.Context, ch domain.Channel) (*domain.Snapshot, error) {
	room, err := s.client.FetchUserRoomByUsername(ctx, ch.Username)
	if err != nil {
		// The channel was confirmed to exist when it was resolved, so no LIVE
		// room means the user has never gone live.
		if errors.Is(err, ErrNoLiveRoom) {
			return nil, nil
		}
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
		URL:       "https://www.tiktok.com/@" + ch.Username + "/live",
	}, nil
}

// FetchRecording always returns nil: TikTok LIVE replays are not public
func (s *Source) FetchRecording(ctx context.Context, ch domain.Channel, streamID string) (*domain.Recording, error) {
	return nil, domain.ErrNoRecordings
}
