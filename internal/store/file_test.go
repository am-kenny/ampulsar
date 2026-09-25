package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/am-kenny/ampulsar/internal/domain"
	"github.com/am-kenny/ampulsar/internal/store"
)

// Session

func TestFileRoundTripSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")

	s1 := mustNewFile(t, path)

	want := domain.Session{StreamID: "s1", LiveMessage: domain.MessageRef{ID: "123", ChatID: "123"}, Title: "Factorio"}
	mustSetSession(t, s1, want)

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
	mustDeleteSession(t, s1)

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	if len(b) != 0 {
		t.Fatalf("after delete: want empty file, got %s", b)
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
	mustWriteFile(t, testFile, "x")

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
	mustSetSession(t, s, want)

	// Reopen from the same path to confirm persistence
	s2 := mustNewFile(t, path)
	got := s2.GetSession()
	if got == nil || got.StreamID != "s1" {
		t.Fatalf("nested path did not persist: got %+v", got)
	}
}

func TestFileCorruptFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	mustWriteFile(t, path, "{not json")

	if _, err := store.NewFile(path); err == nil {
		t.Fatal("corrupt file: want error, got nil")
	}
}

// Deliveries

func TestFileDeliveriesSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")

	s1 := mustNewFile(t, path)
	mustSetSession(t, s1, domain.Session{StreamID: "s1", Version: 2})

	want := []domain.Delivery{
		{
			StreamID:      "s1",
			Kind:          domain.DeliveryLive,
			State:         domain.DeliveryPublished,
			Ref:           domain.MessageRef{ID: "42", ChatID: "123"},
			SyncedVersion: 2,
		},
		{
			StreamID: "s1",
			Kind:     domain.DeliveryEnded,
			State:    domain.DeliveryPublishing,
		},
	}
	for _, d := range want {
		mustSaveDelivery(t, s1, d)
	}

	s2 := mustNewFile(t, path)

	got := s2.GetDeliveries()
	if len(got) != len(want) {
		t.Fatalf("after reopen: want %d deliveries, got %+v", len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("delivery %d mismatch: want %+v, got %+v", i, want[i], got[i])
		}
	}
}

func TestFileDeliveryUpdateSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")

	s1 := mustNewFile(t, path)
	mustSetSession(t, s1, domain.Session{StreamID: "s1"})
	mustSaveDelivery(t, s1, domain.Delivery{StreamID: "s1", Kind: domain.DeliveryLive, State: domain.DeliveryPublishing})
	mustSaveDelivery(t, s1, domain.Delivery{StreamID: "s1", Kind: domain.DeliveryLive, State: domain.DeliveryPublished})

	s2 := mustNewFile(t, path)

	got := s2.GetDeliveries()
	if len(got) != 1 || got[0].State != domain.DeliveryPublished {
		t.Fatalf("after reopen: want one published live delivery, got %+v", got)
	}
}

func TestFileNewStreamClearsPersistedDeliveries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")

	s1 := mustNewFile(t, path)
	mustSetSession(t, s1, domain.Session{StreamID: "s1"})
	mustSaveDelivery(t, s1, domain.Delivery{StreamID: "s1", Kind: domain.DeliveryLive})
	mustSetSession(t, s1, domain.Session{StreamID: "s2"})

	s2 := mustNewFile(t, path)

	if got := s2.GetDeliveries(); got != nil {
		t.Fatalf("after new stream and reopen: want no deliveries, got %+v", got)
	}
}

func TestFileDeleteSessionClearsPersistedDeliveries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")

	s1 := mustNewFile(t, path)
	mustSetSession(t, s1, domain.Session{StreamID: "s1"})
	mustSaveDelivery(t, s1, domain.Delivery{StreamID: "s1", Kind: domain.DeliveryLive})
	mustDeleteSession(t, s1)

	s2 := mustNewFile(t, path)

	if got := s2.GetDeliveries(); got != nil {
		t.Fatalf("after delete and reopen: want no deliveries, got %+v", got)
	}
}

func TestFileSessionWithoutDeliveriesLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	data := `{"session":{"StreamID":"s1","Title":"Factorio"}}`
	mustWriteFile(t, path, data)

	s := mustNewFile(t, path)

	if got := s.GetSession(); got == nil || got.StreamID != "s1" {
		t.Fatalf("want session s1, got %+v", got)
	}
	if got := s.GetDeliveries(); got != nil {
		t.Fatalf("want no deliveries, got %+v", got)
	}
}
