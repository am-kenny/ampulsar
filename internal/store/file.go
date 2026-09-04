package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/google/renameio/v2/maybe"

	"github.com/am-kenny/ampulsar/internal/domain"
)

const storeFileMode = 0o600

func NewFile(path string) (*Store, error) {
	s := &Store{}

	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("store: cannot read %s: %w", path, err)
	}

	if len(b) > 0 {
		var session domain.Session

		if err := json.Unmarshal(b, &session); err != nil {
			return nil, fmt.Errorf("store: cannot decode %s: %w", path, err)
		}
		s.session = &session
	}

	s.flush = func() error { return writeFile(path, s.session) }

	if err := s.flush(); err != nil {
		return nil, err
	}

	return s, nil
}

func writeFile(path string, session *domain.Session) error {
	var data []byte

	if session != nil {
		var err error

		data, err = json.Marshal(session)
		if err != nil {
			return fmt.Errorf("store: cannot encode session: %w", err)
		}
	}

	err := maybe.WriteFile(path, data, storeFileMode)
	if err != nil {
		return fmt.Errorf("store: cannot write %s: %w", path, err)
	}

	return nil
}
