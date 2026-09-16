package domain

type DeliveryKind string

const (
	DeliveryLive  DeliveryKind = "live"  // posted at live event
	DeliveryEnded DeliveryKind = "ended" // posted at end event
)

type DeliveryState string

const (
	DeliveryPublishing DeliveryState = "publishing"
	DeliveryPublished  DeliveryState = "published"
)

type Delivery struct {
	StreamID string // the owning session ID
	Kind     DeliveryKind
	State    DeliveryState

	Ref           MessageRef
	SyncedVersion int // the Session.Version this message currently shows
}

type MessageRef struct {
	ID     string
	ChatID string
}
