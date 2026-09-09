package telegram

import (
	"context"
	"fmt"
	"strconv"

	"github.com/am-kenny/ampulsar/internal/domain"
)

type Sink struct {
	client *Client
}

func NewSink(client *Client) *Sink {
	return &Sink{client: client}
}

func (s *Sink) Send(ctx context.Context, chatID, text string) (*domain.MessageRef, error) {
	id, err := s.client.SendHTMLMessage(ctx, chatID, text)
	if err != nil {
		return nil, err
	}

	strID := strconv.Itoa(id)

	return &domain.MessageRef{
		ID:     strID,
		ChatID: chatID,
	}, nil
}

func (s *Sink) Edit(ctx context.Context, ref domain.MessageRef, text string) error {
	id, err := strconv.Atoi(ref.ID)
	if err != nil {
		return fmt.Errorf("sink: failed to parse message id %s: %w", ref.ID, err)
	}

	return s.client.EditHTMLMessageText(ctx, ref.ChatID, id, text)
}

func (s *Sink) Delete(ctx context.Context, ref domain.MessageRef) error {
	id, err := strconv.Atoi(ref.ID)
	if err != nil {
		return fmt.Errorf("sink: failed to parse message id %s: %w", ref.ID, err)
	}

	return s.client.DeleteMessage(ctx, ref.ChatID, id)
}

func (s *Sink) Pin(ctx context.Context, ref domain.MessageRef) error {
	id, err := strconv.Atoi(ref.ID)
	if err != nil {
		return fmt.Errorf("sink: failed to parse message id %s: %w", ref.ID, err)
	}

	return s.client.PinChatMessage(ctx, ref.ChatID, id)
}

func (s *Sink) Unpin(ctx context.Context, ref domain.MessageRef) error {
	id, err := strconv.Atoi(ref.ID)
	if err != nil {
		return fmt.Errorf("sink: failed to parse message id %s: %w", ref.ID, err)
	}

	return s.client.UnpinChatMessage(ctx, ref.ChatID, id)
}
