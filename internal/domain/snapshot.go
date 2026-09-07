package domain

import "time"

type Snapshot struct {
	StreamID  string
	Title     string
	Game      string
	StartedAt time.Time
}
