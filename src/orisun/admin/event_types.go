package admin

type Event struct {
	EventType string      `json:"event_type"`
	Data      interface{} `json:"data"`
}