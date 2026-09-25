package store_test

import (
	"os"
	"testing"

	"github.com/am-kenny/ampulsar/internal/domain"
	"github.com/am-kenny/ampulsar/internal/store"
)

// mustSetSession stores session and fails the test if that errors
func mustSetSession(t *testing.T, s *store.Store, session domain.Session) {
	t.Helper()
	if err := s.SetSession(session); err != nil {
		t.Fatalf("SetSession: %v", err)
	}
}

// mustSaveDelivery stores delivery and fails the test if that errors
func mustSaveDelivery(t *testing.T, s *store.Store, d domain.Delivery) {
	t.Helper()
	if err := s.SaveDelivery(d); err != nil {
		t.Fatalf("SaveDelivery: %v", err)
	}
}

// mustNewFile initializes new file store and fails the test if that errors
func mustNewFile(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.NewFile(path)
	if err != nil {
		t.Fatalf("NewFile(%s): %v", path, err)
	}
	return s
}

// mustWriteFile writes data to path and fails the test if that errors
func mustWriteFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

// mustDeleteSession deletes session and fails the test if that errors
func mustDeleteSession(t *testing.T, s *store.Store) {
	t.Helper()
	if err := s.DeleteSession(); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
}
