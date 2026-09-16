package store

import (
	"errors"
	"sync"

	"github.com/am-kenny/ampulsar/internal/domain"
)

var (
	ErrNoSession     = errors.New("store: no session")
	ErrStaleDelivery = errors.New("store: delivery does not belong to the current session")
)

type Store struct {
	mu         sync.RWMutex
	session    *domain.Session
	deliveries []domain.Delivery
	flush      func() error // nil => no persistence
}

func (s *Store) GetSession() *domain.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.session == nil {
		return nil
	}

	cp := *s.session
	return &cp
}

// SetSession sets provided session in store and deletes deliveries if session has a new ID
func (s *Store) SetSession(session domain.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.session != nil && s.session.StreamID != session.StreamID {
		s.deliveries = nil
	}

	s.session = &session
	return s.persist()
}

// DeleteSession deletes session and deliveries from store
func (s *Store) DeleteSession() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.session = nil
	s.deliveries = nil
	return s.persist()
}

func (s *Store) GetDeliveries() []domain.Delivery {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.deliveries) == 0 {
		return nil
	}

	cp := make([]domain.Delivery, len(s.deliveries))
	copy(cp, s.deliveries)
	return cp
}

func (s *Store) SaveDelivery(d domain.Delivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.session == nil {
		return ErrNoSession
	}
	if d.StreamID != s.session.StreamID {
		return ErrStaleDelivery
	}

	for i := range s.deliveries {
		if s.deliveries[i].Kind == d.Kind {
			s.deliveries[i] = d
			return s.persist()
		}
	}

	s.deliveries = append(s.deliveries, d)
	return s.persist()
}

func (s *Store) persist() error {
	if s.flush != nil {
		return s.flush()
	}
	return nil
}
