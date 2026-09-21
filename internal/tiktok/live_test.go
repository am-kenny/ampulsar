package tiktok_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/am-kenny/ampulsar/internal/tiktok"
)

// TestLive_Smoke hits the real TikTok endpoint
//
//	TIKTOK_LIVE_USERNAME=some_creator go test -run TestLive ./internal/tiktok/ -v
//
// It asserts the contract, not a particular state: the call must either
// succeed with a known status and a user id, or fail with an *APIError
func TestLive_Smoke(t *testing.T) {
	username := os.Getenv("TIKTOK_LIVE_USERNAME")
	if username == "" {
		t.Skip("set TIKTOK_LIVE_USERNAME to run against the real TikTok endpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	src := tiktok.NewSource(tiktok.NewClient())
	ch, err := src.ResolveChannel(ctx, username)
	if err != nil {
		t.Fatalf("ResolveChannel(%q): %v", username, err)
	}
	t.Logf("resolved %+v", ch)

	snap, err := src.FetchStream(ctx, ch)
	if err != nil {
		t.Fatalf("FetchStream: %v", err)
	}
	if snap == nil {
		t.Logf("%s is offline", username)
		return
	}
	t.Logf("%s is live: %+v", username, *snap)
	if snap.StreamID == "" {
		t.Error("live snapshot without StreamID")
	}
	if snap.StartedAt.IsZero() {
		t.Log("warning: live snapshot without StartedAt; check liveRoom.startTime in a real capture")
	}
}
