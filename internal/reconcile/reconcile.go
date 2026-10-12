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
	kick   chan struct{}
}

func NewReconciler(sink Sink, store Store, config Config, opts ...Option) *Reconciler {
	r := &Reconciler{
		sink:   sink,
		store:  store,
		config: config,
		now:    time.Now,
		kick:   make(chan struct{}, 1),
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

// Kick asks for a sync as soon as possible without blocking.
func (r *Reconciler) Kick() {
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// Run syncs once, then on every interval or kick until ctx is done
func (r *Reconciler) Run(ctx context.Context) {
	r.Sync(ctx)

	ticker := time.NewTicker(r.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-r.kick:
		}

		r.Sync(ctx)
	}
}

func (r *Reconciler) Sync(ctx context.Context) {
	session := r.store.GetSession()
	if session == nil {
		return
	}

	live, ok := r.liveDelivery()

	switch {
	case session.State == domain.SessionLive:
		if !ok {
			if live, ok = r.publishLive(ctx, session); !ok {
				return
			}
		}
		r.pinLive(ctx, &live)
		r.syncLive(ctx, session, &live)
	case !ok:
		// the stream ended before it was announced
	default:
		r.endLive(ctx, session, &live)
	}
}

// publishLive sends the message of type Live
func (r *Reconciler) publishLive(ctx context.Context, session *domain.Session) (domain.Delivery, bool) {
	cfg := r.config

	streamEvent := message.StreamEvent{Session: *session, Timestamp: r.now().Unix()}

	text, err := message.FormatLive(cfg.Style, cfg.Lang, streamEvent)
	if err != nil {
		slog.Warn("reconciler: message formatting failed", "err", err, "stream_event", streamEvent)
		return domain.Delivery{}, false
	}

	messageRef, err := r.sink.Send(ctx, cfg.ChatID, text)
	if err != nil {
		slog.Warn("reconciler: message send failed", "err", err, "chat_id", cfg.ChatID)
		return domain.Delivery{}, false
	}

	d := domain.Delivery{
		StreamID:      session.StreamID,
		Kind:          domain.DeliveryLive,
		State:         domain.DeliveryPublished,
		Ref:           messageRef,
		SyncedVersion: session.Version,
	}

	if err := r.store.SaveDelivery(d); err != nil {
		slog.Warn("reconciler: save delivery failed", "err", err, "delivery", d)
	}

	return d, true
}

// pinLive pins the message of type Live if configured and not pinned yet
func (r *Reconciler) pinLive(ctx context.Context, live *domain.Delivery) {
	if !r.config.Pin || live.Pinned {
		return
	}

	if err := r.sink.Pin(ctx, live.Ref); err != nil {
		slog.Warn("reconciler: pin message failed", "err", err, "chat_id", live.Ref.ChatID)
		return
	}

	live.Pinned = true

	if err := r.store.SaveDelivery(*live); err != nil {
		slog.Warn("reconciler: save delivery failed", "err", err, "kind", live.Kind)
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

// syncLive edits the message of type Live if it is older version than session and if live editing is enabled
func (r *Reconciler) syncLive(ctx context.Context, session *domain.Session, live *domain.Delivery) {
	if !r.config.EditOnChange || live.SyncedVersion == session.Version {
		return
	}

	streamEvent := message.StreamEvent{Session: *session, Timestamp: r.now().Unix()}

	text, err := message.FormatLive(r.config.Style, r.config.Lang, streamEvent)
	if err != nil {
		// formatting is deterministic, do not retry
		slog.Warn("reconciler: message formatting failed", "err", err, "stream_event", streamEvent)
	} else if err = r.sink.Edit(ctx, live.Ref, text); err != nil {
		slog.Warn("reconciler: message live edit failed", "err", err, "chat_id", live.Ref.ChatID)
		return
	}

	live.SyncedVersion = session.Version

	if err := r.store.SaveDelivery(*live); err != nil {
		slog.Warn("reconciler: save delivery failed", "err", err, "kind", live.Kind)
	}
}

// endLive unpins the message of type Live and runs the end policy
func (r *Reconciler) endLive(ctx context.Context, session *domain.Session, live *domain.Delivery) {
	if live.State == domain.DeliveryDone {
		return
	}

	if !r.unpinLive(ctx, live) {
		return
	}

	if session.State == domain.SessionEnded {
		// wait for the recording
		return
	}

	if err := r.finalize(ctx, session, live); err != nil {
		slog.Warn("reconciler: finalize failed", "err", err, "stream_id", live.StreamID)
		return
	}

	live.State = domain.DeliveryDone

	if err := r.store.SaveDelivery(*live); err != nil {
		slog.Warn("reconciler: save delivery failed", "err", err, "kind", live.Kind)
	}
}
// unpinLive unpins the message of type Live if configured
func (r *Reconciler) unpinLive(ctx context.Context, live *domain.Delivery) bool {
	if !live.Pinned {
		return true
	}

	if err := r.sink.Unpin(ctx, live.Ref); err != nil {
		slog.Warn("reconciler: unpin message failed", "err", err, "chat_id", live.Ref.ChatID)
		return false
	}

	live.Pinned = false

	if err := r.store.SaveDelivery(*live); err != nil {
		slog.Warn("reconciler: save delivery failed", "err", err, "kind", live.Kind)
	}

	return true
}

// finalize runs end policy
func (r *Reconciler) finalize(ctx context.Context, session *domain.Session, live *domain.Delivery) error {
	cfg := r.config

	switch cfg.OnEnd {
	case domain.EndPolicyEditInPlace, domain.EndPolicyNewMessage, domain.EndPolicyReplace:
		streamEvent := message.StreamEvent{Session: *session, Timestamp: r.now().Unix()}

		text, err := message.FormatWentOffline(cfg.Style, cfg.Lang, streamEvent)
		if err != nil {
			// formatting is deterministic
			slog.Warn("reconciler: message formatting failed", "err", err, "stream_event", streamEvent)
			return nil
		}

		switch cfg.OnEnd {
		case domain.EndPolicyEditInPlace:
			return r.sink.Edit(ctx, live.Ref, text)
		case domain.EndPolicyReplace:
			if err := r.sink.Delete(ctx, live.Ref); err != nil {
				return err
			}
		}

		_, err = r.sink.Send(ctx, cfg.ChatID, text)
		return err

	case domain.EndPolicyDelete:
		return r.sink.Delete(ctx, live.Ref)
	case domain.EndPolicyNone:
		// no extra actions
	}

	return nil
}
