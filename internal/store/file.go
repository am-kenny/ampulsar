package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/renameio/v2/maybe"

	"github.com/am-kenny/ampulsar/internal/domain"
)

const (
	storeFileMode = 0o600
	storeDirMode  = 0o700
)

// shape of the state file
type fileState struct {
	Session    *domain.Session   `json:"session"`
	Deliveries []domain.Delivery `json:"deliveries,omitempty"`
}

func NewFile(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, storeDirMode); err != nil {
			return nil, fmt.Errorf("store: cannot create dir %s: %w", dir, err)
		}
	}

	s := &Store{}

	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("store: cannot read %s: %w", path, err)
	}

	if len(b) > 0 {
		var state fileState

		if err := json.Unmarshal(b, &state); err != nil {
			return nil, fmt.Errorf("store: cannot decode %s: %w", path, err)
		}
		s.session = state.Session
		s.deliveries = state.Deliveries
	}

	s.flush = func() error { return writeFile(path, s.session, s.deliveries) }

	if err := s.flush(); err != nil {
		return nil, err
	}

	return s, nil
}

func writeFile(path string, session *domain.Session, deliveries []domain.Delivery) error {
	var data []byte

	if session != nil {
		var err error

		data, err = json.Marshal(fileState{Session: session, Deliveries: deliveries})
		if err != nil {
			return fmt.Errorf("store: cannot encode state: %w", err)
		}
	}

	err := maybe.WriteFile(path, data, storeFileMode)
	if err != nil {
		return fmt.Errorf("store: cannot write %s: %w", path, err)
	}

	return nil
}
