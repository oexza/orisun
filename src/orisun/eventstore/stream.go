package eventstore

import (
	"context"
)

// CustomEventStream implements the EventStore_CatchUpSubscribeToEventsServer interface
// for direct usage without going through gRPC server
type CustomEventStream struct {
	ctx    context.Context
	events chan *Event
}

// NewCustomEventStream creates a new CustomEventStream instance
func NewCustomEventStream(ctx context.Context) *CustomEventStream {
	return &CustomEventStream{
		ctx:    ctx,
		events: make(chan *Event, 100), // Buffered channel to prevent blocking
	}
}

// Send implements the Send method required by EventStore_CatchUpSubscribeToEventsServer
func (s *CustomEventStream) Send(event *Event) error {
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case s.events <- event:
		return nil
	}
}

// Context implements the Context method required by EventStore_CatchUpSubscribeToEventsServer
func (s *CustomEventStream) Context() context.Context {
	return s.ctx
}

// Events returns the channel for consuming events
func (s *CustomEventStream) Events() <-chan *Event {
	return s.events
}

// RecvMsg implements the grpc.ServerStream interface
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

// SendMsg implements the grpc.ServerStream interface
func (s *CustomEventStream) SendMsg(m interface{}) error {
	return nil
}
