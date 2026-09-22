package domain

import "time"

type SessionState string

const (
	SessionLive     SessionState = "live"
	SessionEnded    SessionState = "ended"
	SessionArchived SessionState = "archived"
	SessionClosed   SessionState = "closed"
)

type Session struct {
	Channel

	StreamID    string
	State       SessionState
	Version     int
	LiveMessage MessageRef

	Title string
	Game  string
	URL   string

	EndedAt   time.Time
	Recording Recording
}

type Recording struct {
	URL      string
	Title    string
	Duration time.Duration
}
