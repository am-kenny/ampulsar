package observer

import "github.com/am-kenny/ampulsar/internal/client"

type TwitchObserver struct {
	isLive   bool
	streamID string
	client   *client.TwitchClient
}

func NewTwitchObserver() {

}
