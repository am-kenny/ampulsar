package store

import (
	"sync"

	"github.com/am-kenny/ampulsar/internal/domain"
)

type Store struct {
	mu      sync.RWMutex
	session *domain.Session
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

func (s *Store) SetSession(session domain.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.session = &session
}

func (s *Store) DeleteSession() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.session = nil
}
