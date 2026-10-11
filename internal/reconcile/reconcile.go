package reconcile

import (
	"context"
	"time"

	"github.com/am-kenny/ampulsar/internal/domain"
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
}
