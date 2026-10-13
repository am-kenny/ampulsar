package poll

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/am-kenny/ampulsar/internal/domain"
)

type Source interface {
	FetchStream(ctx context.Context, channel domain.Channel) (*domain.Snapshot, error)
	FetchRecording(ctx context.Context, channel domain.Channel, streamID string) (*domain.Recording, error)
}

type Store interface {
	GetSession() *domain.Session
	SetSession(domain.Session) error
	DeleteSession() error
	GetDeliveries() []domain.Delivery
}

type Config struct {
	EndGrace         time.Duration
	WaitForRecording bool
}

type Poller struct {
	source  Source
	store   Store
	channel domain.Channel
	config  Config
	now     func() time.Time // function to retrieve current time
}

func NewPoller(source Source, store Store, channel domain.Channel, config Config, opts ...Option) *Poller {
	p := &Poller{
		source:  source,
		store:   store,
		channel: channel,
		config:  config,
		now:     time.Now,
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

type Option func(*Poller)

func WithClock(now func() time.Time) Option {
	return func(p *Poller) { p.now = now }
}

func (p *Poller) Poll(ctx context.Context) {
	snapshot, err := p.source.FetchStream(ctx, p.channel)
	if err != nil {
		slog.Error("poller: fetch stream failed", "err", err, "channel", p.channel.Username)
		return
	}

	session := p.store.GetSession()

	switch {
	case session == nil && snapshot != nil:
		// went live
		p.startSession(snapshot)
	case session == nil:
		// offline and nothing to track
	case session.State == domain.SessionLive && snapshot == nil:
		// went offline
		p.endSession(ctx, session)
	case session.State == domain.SessionLive && session.StreamID != snapshot.StreamID:
		// stream restarted between ticks
		p.endSession(ctx, session)
	case session.State == domain.SessionEnded:
		// waiting for the recording
		p.awaitRecording(ctx, session)
	case session.State != domain.SessionLive:
		// archived or closed, waiting for the end actions
		p.closeSession(session)
	default:
		// still live with the same StreamID
		p.handleLive(session, snapshot)
	}
}

func (p *Poller) startSession(snapshot *domain.Snapshot) {
	slog.Info("poller: stream went live", "snapshot", snapshot)

	p.save(&domain.Session{
		Channel:  p.channel,
		StreamID: snapshot.StreamID,
		State:    domain.SessionLive,
		Version:  1,
		Title:    snapshot.Title,
		Game:     snapshot.Game,
		URL:      snapshot.URL,
	})
}

// handleLive applies stream info changes to the session
func (p *Poller) handleLive(session *domain.Session, snapshot *domain.Snapshot) {
	if session.Title == snapshot.Title && session.Game == snapshot.Game {
		return
	}

	slog.Info("poller: stream info changed", "stream_id", session.StreamID, "title", snapshot.Title, "game", snapshot.Game)
	session.Title = snapshot.Title
	session.Game = snapshot.Game
	session.Version++

	p.save(session)
}

// endSession marks the session as ended and looks for the recording right away if the end policy waits for it
func (p *Poller) endSession(ctx context.Context, session *domain.Session) {
	slog.Info("poller: stream went offline")

	session.EndedAt = p.now()

	if !p.config.WaitForRecording {
		session.State = domain.SessionClosed
		p.save(session)
		return
	}

	session.State = domain.SessionEnded
	p.save(session)
	p.awaitRecording(ctx, session)
}

// awaitRecording fetches the recording and checks if the end grace is over
func (p *Poller) awaitRecording(ctx context.Context, session *domain.Session) {
	expired := p.now().Sub(session.EndedAt) >= p.config.EndGrace

	recording, err := p.source.FetchRecording(ctx, p.channel, session.StreamID)

	switch {
	case errors.Is(err, domain.ErrNoRecordings):
		slog.Debug("poller: source has no recordings, finalizing now", "platform", session.Platform)
		session.State = domain.SessionClosed
	case err != nil:
		slog.Warn("poller: fetch recording failed", "err", err, "stream_id", session.StreamID)
		if !expired {
			return
		}
		session.State = domain.SessionClosed
	case recording != nil:
		session.Recording = *recording
		session.Version++
		session.State = domain.SessionArchived
	case expired:
		slog.Info("poller: recording grace expired", "stream_id", session.StreamID)
		session.State = domain.SessionClosed
	default:
		// keep waiting
		return
	}

	p.save(session)
}

// closeSession deletes the session once all deliveries are done or the grace period is over
func (p *Poller) closeSession(session *domain.Session) {
	expired := p.now().Sub(session.EndedAt) >= p.config.EndGrace

	if !allDone(p.store.GetDeliveries()) && !expired {
		return
	}

	if err := p.store.DeleteSession(); err != nil {
		slog.Warn("poller: delete session failed", "err", err)
	}
}

// save stores the session
func (p *Poller) save(session *domain.Session) {
	if err := p.store.SetSession(*session); err != nil {
		slog.Warn("poller: set session failed", "err", err, "stream_id", session.StreamID)
	}
}

// allDone returns true if every delivery is of state done
func allDone(deliveries []domain.Delivery) bool {
	for _, d := range deliveries {
		if d.State != domain.DeliveryDone {
			return false
		}
	}
	return true
}
