package store_test

import (
	"errors"
	"testing"

	"github.com/am-kenny/ampulsar/internal/domain"
	"github.com/am-kenny/ampulsar/internal/store"
)

// Session

func TestMemoryGetEmptyReturnsNil(t *testing.T) {
	var s store.Store
	if got := s.GetSession(); got != nil {
		t.Fatalf("empty store: want nil, got %+v", got)
	}
}

func TestMemorySetGetRoundTrip(t *testing.T) {
	var s store.Store
	want := domain.Session{StreamID: "s1", LiveMessage: domain.MessageRef{ID: "42", ChatID: "123"}, Title: "Factorio"}

	mustSetSession(t, &s, want)

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
	mustDeleteSession(t, &s)

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

// Deliveries

func TestMemoryGetDeliveriesEmptyReturnsNil(t *testing.T) {
	var s store.Store
	mustSetSession(t, &s, domain.Session{StreamID: "s1"})

	if got := s.GetDeliveries(); got != nil {
		t.Fatalf("no deliveries: want nil, got %+v", got)
	}
}

func TestMemorySaveDeliveryWithoutSessionFails(t *testing.T) {
	var s store.Store

	err := s.SaveDelivery(domain.Delivery{StreamID: "s1", Kind: domain.DeliveryLive})
	if !errors.Is(err, store.ErrNoSession) {
		t.Fatalf("want ErrNoSession, got %v", err)
	}
}

func TestMemorySaveDeliveryForOtherStreamFails(t *testing.T) {
	var s store.Store
	mustSetSession(t, &s, domain.Session{StreamID: "s1"})

	err := s.SaveDelivery(domain.Delivery{StreamID: "s2", Kind: domain.DeliveryLive})
	if !errors.Is(err, store.ErrStaleDelivery) {
		t.Fatalf("want ErrStaleDelivery, got %v", err)
	}
	if got := s.GetDeliveries(); got != nil {
		t.Fatalf("rejected delivery was stored: %+v", got)
	}
}

func TestMemorySaveDeliveryKeepsOnePerKind(t *testing.T) {
	var s store.Store
	mustSetSession(t, &s, domain.Session{StreamID: "s1"})

	mustSaveDelivery(t, &s, domain.Delivery{StreamID: "s1", Kind: domain.DeliveryLive, State: domain.DeliveryPublishing})
	mustSaveDelivery(t, &s, domain.Delivery{StreamID: "s1", Kind: domain.DeliveryEnded, State: domain.DeliveryPublishing})
	mustSaveDelivery(t, &s, domain.Delivery{StreamID: "s1", Kind: domain.DeliveryLive, State: domain.DeliveryPublished})

	got := s.GetDeliveries()
	if len(got) != 2 {
		t.Fatalf("want 2 deliveries, got %+v", got)
	}
	if got[0].Kind != domain.DeliveryLive || got[0].State != domain.DeliveryPublished {
		t.Fatalf("live delivery not replaced in place: got %+v", got[0])
	}
	if got[1].Kind != domain.DeliveryEnded {
		t.Fatalf("ended delivery: got %+v", got[1])
	}
}

func TestMemorySameStreamKeepsDeliveries(t *testing.T) {
	var s store.Store
	mustSetSession(t, &s, domain.Session{StreamID: "s1", Version: 1})
	mustSaveDelivery(t, &s, domain.Delivery{StreamID: "s1", Kind: domain.DeliveryLive})

	mustSetSession(t, &s, domain.Session{StreamID: "s1", Version: 2})

	if got := s.GetDeliveries(); len(got) != 1 {
		t.Fatalf("session update of same stream: want 1 delivery, got %+v", got)
	}
}

func TestMemoryNewStreamClearsDeliveries(t *testing.T) {
	var s store.Store
	mustSetSession(t, &s, domain.Session{StreamID: "s1"})
	mustSaveDelivery(t, &s, domain.Delivery{StreamID: "s1", Kind: domain.DeliveryLive})

	mustSetSession(t, &s, domain.Session{StreamID: "s2"})

	if got := s.GetDeliveries(); got != nil {
		t.Fatalf("after new stream: want no deliveries, got %+v", got)
	}
}

func TestMemoryDeleteSessionClearsDeliveries(t *testing.T) {
	var s store.Store
	mustSetSession(t, &s, domain.Session{StreamID: "s1"})
	mustSaveDelivery(t, &s, domain.Delivery{StreamID: "s1", Kind: domain.DeliveryLive})
	mustDeleteSession(t, &s)

	if got := s.GetDeliveries(); got != nil {
		t.Fatalf("after delete: want no deliveries, got %+v", got)
	}
}

// Test immutability
func TestGetDeliveriesReturnsCopy(t *testing.T) {
	var s store.Store
	mustSetSession(t, &s, domain.Session{StreamID: "s1"})
	mustSaveDelivery(t, &s, domain.Delivery{StreamID: "s1", Kind: domain.DeliveryLive, State: domain.DeliveryPublishing})

	got := s.GetDeliveries()
	got[0].State = domain.DeliveryPublished

	again := s.GetDeliveries()
	if again[0].State != domain.DeliveryPublishing {
		t.Fatalf("stored state was mutated through returned slice: got %q", again[0].State)
	}
}
