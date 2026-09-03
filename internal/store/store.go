package store

import (
	"sync"

	"github.com/am-kenny/ampulsar/internal/domain"
)

type Store struct {
	mu      sync.RWMutex
	session *domain.Session
	flush   func() error // nil => no persistence
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

func (s *Store) SetSession(session domain.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.session = &session
	return s.persist()
}

func (s *Store) DeleteSession() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.session = nil
	return s.persist()
}

func (s *Store) persist() error {
	if s.flush != nil {
		return s.flush()
	}
	return nil
}
