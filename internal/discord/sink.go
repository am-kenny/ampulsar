package discord

import (
	"context"
	"fmt"

	dgo "github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/am-kenny/ampulsar/internal/domain"
)

type Sink struct {
	rest rest.Rest
}

func NewSink(r rest.Rest) *Sink {
	return &Sink{rest: r}
}

func (s *Sink) Send(ctx context.Context, chatID, text string) (domain.MessageRef, error) {
	cID, err := parseID(chatID)
	if err != nil {
		return domain.MessageRef{}, err
	}

	msg, err := s.rest.CreateMessage(cID, dgo.MessageCreate{Content: text}, rest.WithCtx(ctx))
	if err != nil {
		return domain.MessageRef{}, fmt.Errorf("discord: create message: %w", err)
	}

	return domain.MessageRef{
		ID:     msg.ID.String(),
		ChatID: chatID,
	}, nil
}

func (s *Sink) Edit(ctx context.Context, ref domain.MessageRef, text string) error {
	cID, mID, err := parseRef(ref)
	if err != nil {
		return err
	}

	if _, err = s.rest.UpdateMessage(cID, mID, dgo.MessageUpdate{Content: &text}, rest.WithCtx(ctx)); err != nil {
		return fmt.Errorf("discord: update message: %w", err)
	}
	return nil
}

func (s *Sink) Delete(ctx context.Context, ref domain.MessageRef) error {
	cID, mID, err := parseRef(ref)
	if err != nil {
		return err
	}

	if err = s.rest.DeleteMessage(cID, mID, rest.WithCtx(ctx)); err != nil {
		return fmt.Errorf("discord: delete message: %w", err)
	}
	return nil
}

func (s *Sink) Pin(ctx context.Context, ref domain.MessageRef) error {
	cID, mID, err := parseRef(ref)
	if err != nil {
		return err
	}

	if err = s.rest.PinMessage(cID, mID, rest.WithCtx(ctx)); err != nil {
		return fmt.Errorf("discord: pin message: %w", err)
	}
	return nil
}

func (s *Sink) Unpin(ctx context.Context, ref domain.MessageRef) error {
	cID, mID, err := parseRef(ref)
	if err != nil {
		return err
	}

	if err = s.rest.UnpinMessage(cID, mID, rest.WithCtx(ctx)); err != nil {
		return fmt.Errorf("discord: unpin message: %w", err)
	}
	return nil
}

func parseRef(ref domain.MessageRef) (chatID, msgID snowflake.ID, err error) {
	if chatID, err = parseID(ref.ChatID); err != nil {
		return 0, 0, err
	}
	if msgID, err = parseID(ref.ID); err != nil {
		return 0, 0, err
	}
	return chatID, msgID, nil
}

func parseID(strID string) (snowflake.ID, error) {
	id, err := snowflake.Parse(strID)
	if err != nil {
		return 0, fmt.Errorf("discord: parse id %q: %w", strID, err)
	}
	return id, nil
}
