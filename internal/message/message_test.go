package message_test

import (
	"strings"
	"testing"
	"time"

	"github.com/am-kenny/ampulsar/internal/domain"
	"github.com/am-kenny/ampulsar/internal/message"
)

func sampleEvent() message.StreamEvent {
	return message.StreamEvent{
		Session: domain.Session{
			Channel: domain.Channel{
				Platform:    domain.Twitch,
				ID:          "test_id",
				Username:    "streamer",
				DisplayName: "Streamer",
			},
			Title: "🔴 Fun Title",
			Game:  "Factorio",
			Recording: domain.Recording{
				URL:      "https://vod",
				Title:    "VOD Title",
				Duration: 1*time.Hour + 2*time.Minute + 3*time.Second,
			},
		},
		Timestamp: 1700000000,
	}
}

func TestFormatLive(t *testing.T) {
	for _, style := range []string{"default", "simplified"} {
		for _, lang := range []string{"eng", "ru"} {
			out, err := message.FormatLive(style, lang, sampleEvent())
			if err != nil {
				t.Errorf("FormatLive(%q,%q): %v", style, lang, err)
				continue
			}
			if !strings.Contains(out, "streamer") {
				t.Errorf("FormatLive(%q,%q) missing channel url: %q", style, lang, out)
			}
		}
	}
}

func TestFormatWentOffline(t *testing.T) {
	for _, lang := range []string{"eng", "ru"} {
		out, err := message.FormatWentOffline("default", lang, sampleEvent())
		if err != nil {
			t.Errorf("FormatWentOffline(default,%q): %v", lang, err)
			continue
		}
		if !strings.Contains(out, "https://vod") {
			t.Errorf("FormatWentOffline(default,%q) missing recording url: %q", lang, out)
		}
	}
}

func TestFormatUnknownTemplate(t *testing.T) {
	if _, err := message.FormatLive("unknown", "eng", sampleEvent()); err == nil {
		t.Fatal("want error for missing live_unknown template, got nil")
	}
	if _, err := message.FormatWentOffline("unknown", "eng", sampleEvent()); err == nil {
		t.Fatal("want error for missing offline_unknown template, got nil")
	}
}

func TestRenderContract(t *testing.T) {
	const contract = `title={{.Title}}
display={{.DisplayName}}
user={{.Username}}
game={{.Game}}
ts={{.Timestamp}}
trimmed={{trimRedDot .Title}}
rec_title={{.Recording.Title}}
rec_url={{.Recording.URL}}
rec_dur={{hms .Recording.Duration}}`

	const want = `title=🔴 Fun Title
display=Streamer
user=streamer
game=Factorio
ts=1700000000
trimmed= Fun Title
rec_title=VOD Title
rec_url=https://vod
rec_dur=1:02:03`

	got, err := message.Render(contract, sampleEvent())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != want {
		t.Errorf("output mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderParseError(t *testing.T) {
	_, err := message.Render(`{{.Title`, message.StreamEvent{})
	if err == nil {
		t.Fatal("want parse error for malformed template, got nil")
	}
}
