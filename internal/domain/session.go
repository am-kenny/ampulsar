package domain

import "time"

type Session struct {
	Channel

	StreamID      string
	LiveMessageID int

	Title string
	Game  string

	Recording Recording
}

type Recording struct {
	URL      string
	Title    string
	Duration time.Duration
}
