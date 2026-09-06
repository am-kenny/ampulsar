package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/am-kenny/ampulsar/internal/domain"
	"github.com/am-kenny/ampulsar/internal/store"
)

// Memory behavior

func TestMemoryGetEmptyReturnsNil(t *testing.T) {
	var s store.Store
	if got := s.GetSession(); got != nil {
		t.Fatalf("empty store: want nil, got %+v", got)
	}
}

func TestMemorySetGetRoundTrip(t *testing.T) {
	var s store.Store
	want := domain.Session{StreamID: "s1", LiveMessageID: 42, Title: "Factorio"}

	if err := s.SetSession(want); err != nil {
		t.Fatalf("SetSession: %v", err)
	}

	got := s.GetSession()
	if got == nil {
		t.Fatal("after SetSession: got nil")
	}
	if *got != want {
		t.Fatalf("round trip mismatch: want %+v, got %+v", want, *got)
	}
}

func TestMemoryDelete(t *testing.T) {
	var s store.Store
	mustSetSession(t, &s, domain.Session{StreamID: "s1"})

	if err := s.DeleteSession(); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if got := s.GetSession(); got != nil {
		t.Fatalf("after delete: want nil, got %+v", got)
	}
}

// Test immutability
func TestGetSessionReturnsCopy(t *testing.T) {
	var s store.Store
	mustSetSession(t, &s, domain.Session{StreamID: "s1", Title: "original"})

	got := s.GetSession()
	got.Title = "mutated"

	again := s.GetSession()
	if again.Title != "original" {
		t.Fatalf("stored state was mutated through returned pointer: got %q", again.Title)
	}
}

// File persistence

func TestFileRoundTripSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")

	s1 := mustNewFile(t, path)

	want := domain.Session{StreamID: "s1", LiveMessageID: 123, Title: "Factorio"}
	if err := s1.SetSession(want); err != nil {
		t.Fatalf("SetSession: %v", err)
	}

	s2 := mustNewFile(t, path)

	got := s2.GetSession()
	if got == nil {
		t.Fatal("after reopen: got nil")
	}
	if *got != want {
		t.Fatalf("reopen mismatch: want %+v, got %+v", want, *got)
	}
}

func TestFileMissingIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	s := mustNewFile(t, path)

	if got := s.GetSession(); got != nil {
		t.Fatalf("missing file: want empty, got %+v", got)
	}
}

func TestFileDeleteEmptiesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")

	s1 := mustNewFile(t, path)
	mustSetSession(t, s1, domain.Session{StreamID: "s1"})

	if err := s1.DeleteSession(); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	s2 := mustNewFile(t, path)

	if got := s2.GetSession(); got != nil {
		t.Fatalf("after delete+reopen: want empty, got %+v", got)
	}
}

func TestFileNewFileUnwritableFails(t *testing.T) {
	// A path whose parent is a file, not a directory, cannot be written
	dir := t.TempDir()
	testFile := filepath.Join(dir, "file")
	if err := os.WriteFile(testFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(testFile, "session.json") // appending file to another file

	if _, err := store.NewFile(badPath); err == nil {
		t.Fatal("NewFile on unwritable path: want error, got nil")
	}
}

func TestFileCreatesMissingParentDir(t *testing.T) {
	// A path several levels deep, none of which exist yet
	path := filepath.Join(t.TempDir(), "a", "b", "c", "session.json")

	s := mustNewFile(t, path)

	want := domain.Session{StreamID: "s1", Title: "Factorio"}
	if err := s.SetSession(want); err != nil {
		t.Fatalf("SetSession into created dir: %v", err)
	}

	// Reopen from the same path to confirm persistence
	s2 := mustNewFile(t, path)
	got := s2.GetSession()
	if got == nil || got.StreamID != "s1" {
		t.Fatalf("nested path did not persist: got %+v", got)
	}
}

// mustSet stores session and fails the test if that errors
func mustSetSession(t *testing.T, s *store.Store, session domain.Session) {
	t.Helper()
	if err := s.SetSession(session); err != nil {
		t.Fatalf("setup SetSession: %v", err)
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
