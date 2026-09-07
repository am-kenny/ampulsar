package domain

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
	Duration string
}
