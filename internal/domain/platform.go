package domain

import "errors"

type Platform string

const (
	Twitch  Platform = "twitch"
	YouTube Platform = "youtube"
	TikTok  Platform = "tiktok"
)

var ErrNoRecordings = errors.New("platform does not publish recordings")
