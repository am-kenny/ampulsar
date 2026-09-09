package domain

import "time"

type Session struct {
	Channel

	StreamID    string
	LiveMessage MessageRef

	Title string
	Game  string

	Recording Recording
}

type MessageRef struct {
	ID     string
	ChatID string
}

type Recording struct {
	URL      string
	Title    string
	Duration time.Duration
}
