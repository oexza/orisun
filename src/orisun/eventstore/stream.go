package eventstore

import (
	"context"
)

type CustomEventStream struct {
	ctx    context.Context
	events chan *Event
}

func NewCustomEventStream(ctx context.Context) *CustomEventStream {
	return &CustomEventStream{
		ctx:    ctx,
		events: make(chan *Event, 100), // Buffered channel to prevent blocking
	}
}

func (s *CustomEventStream) Send(event *Event) error {
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case s.events <- event:
		return nil
	}
}

func (s *CustomEventStream) Context() context.Context {
	return s.ctx
}

// Events returns the channel for consuming events
func (s *CustomEventStream) Events() <-chan *Event {
	return s.events
}

func (s *CustomEventStream) Recv() (*Event, error) {
	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case event, ok := <-s.events:
		if !ok {
			return nil, context.Canceled
		}
		return event, nil
	}
}
