package reconcile

import (
	"context"
	"log/slog"
	"time"

	"github.com/am-kenny/ampulsar/internal/domain"
	"github.com/am-kenny/ampulsar/internal/message"
)

type Sink interface {
	Send(ctx context.Context, chatID, text string) (domain.MessageRef, error)
	Edit(ctx context.Context, ref domain.MessageRef, text string) error
	Delete(ctx context.Context, ref domain.MessageRef) error
	Pin(ctx context.Context, ref domain.MessageRef) error
	Unpin(ctx context.Context, ref domain.MessageRef) error
}

type Store interface {
	GetSession() *domain.Session
	GetDeliveries() []domain.Delivery
	SaveDelivery(domain.Delivery) error
}

type Config struct {
	ChatID       string
	Pin          bool
	EditOnChange bool
	OnEnd        domain.EndPolicy
	Style        string // Template style
	Lang         string // Template language
	Interval     time.Duration
}

type Reconciler struct {
	sink   Sink
	store  Store
	config Config
	now    func() time.Time // function to retrieve current time
}

func NewReconciler(sink Sink, store Store, config Config, opts ...Option) *Reconciler {
	r := &Reconciler{
		sink:   sink,
		store:  store,
		config: config,
		now:    time.Now,
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

type Option func(*Reconciler)

func WithClock(now func() time.Time) Option {
	return func(r *Reconciler) { r.now = now }
}

func (r *Reconciler) Sync(ctx context.Context) {
	session := r.store.GetSession()
	if session == nil {
		return
	}

	_, ok := r.liveDelivery()

	switch {
	case session.State == domain.SessionLive && !ok:
		r.publishLive(ctx, session)
	}
}

// publishLive sends the message of type Live and pins it if configured
func (r *Reconciler) publishLive(ctx context.Context, session *domain.Session) {
	cfg := r.config

	streamEvent := message.StreamEvent{Session: *session, Timestamp: r.now().Unix()}

	text, err := message.FormatLive(cfg.Style, cfg.Lang, streamEvent)
	if err != nil {
		slog.Warn("reconciler: message formatting failed", "err", err, "stream_event", streamEvent)
		return
	}

	messageRef, err := r.sink.Send(ctx, cfg.ChatID, text)
	if err != nil {
		slog.Warn("reconciler: message send failed", "err", err, "chat_id", cfg.ChatID)
		return
	}

	d := domain.Delivery{
		StreamID:      session.StreamID,
		Kind:          domain.DeliveryLive,
		State:         domain.DeliveryPublished,
		Ref:           messageRef,
		SyncedVersion: session.Version,
	}

	if cfg.Pin {
		if err := r.sink.Pin(ctx, d.Ref); err != nil {
			slog.Warn("reconciler: pin message failed", "err", err, "chat_id", cfg.ChatID)
		} else {
			d.Pinned = true
		}
	}

	if err := r.store.SaveDelivery(d); err != nil {
		slog.Warn("reconciler: save delivery failed", "err", err, "delivery", d)
	}
}
// liveDelivery returns the stored delivery of type Live
func (r *Reconciler) liveDelivery() (domain.Delivery, bool) {
	for _, d := range r.store.GetDeliveries() {
		if d.Kind == domain.DeliveryLive {
			return d, true
		}
	}

	return domain.Delivery{}, false
}
