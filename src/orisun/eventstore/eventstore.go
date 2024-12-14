package eventstore

import (
	"context"
	"encoding/json"
	"fmt"

	// reflect "reflect"
	"runtime/debug"
	sync "sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"

	"strings"

	logging "orisun/src/orisun/logging"

	"github.com/nats-io/nats.go/jetstream"
)

type SaveEvents interface {
	Save(ctx context.Context, events *[]EventWithMapTags,
		consistencyCondition *ConsistencyCondition, boundary string) (transactionID string, globalID int64, err error)
}

type GetEvents interface {
	Get(ctx context.Context, req *GetEventsRequest) (*GetEventsResponse, error)
}

type EventStore struct {
	UnimplementedEventStoreServer
	js           jetstream.JetStream
	saveEventsFn SaveEvents
	getEventsFn  GetEvents
}

const (
	eventsStreamPrefix          = "ORISUN_EVENTS"
	EventsSubjectName           = "EVENTS"
	pubsubPrefix                = "orisun_pubsub__"
	activeSubscriptionsKVBucket = "ACTIVE_SUBSCRIPTIONS"
)

var logger logging.Logger

func GetEventsStreamName(boundary string) string {
	return eventsStreamPrefix + "__" + boundary
}

func GetEventsSubjectName(boundary string) string {
	return GetEventsStreamName(boundary) + "." + EventsSubjectName
}

func NewEventStoreServer(
	ctx context.Context,
	js jetstream.JetStream,
	saveEventsFn SaveEvents,
	getEventsFn GetEvents,
	boundaries []string) *EventStore {
	log, err := logging.GlobalLogger()

	if err != nil {
		log.Fatalf("Could not configure logger")
	}

	logger = log
	for _, boundary := range boundaries {
		streamName := GetEventsStreamName(boundary)
		info, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
			Name: streamName,
			Subjects: []string{
				GetEventsSubjectName(boundary),
			},
			// MaxAge: 24 * time.Hour,
		})
		if err != nil {
			log.Fatalf("failed to add stream: %v %v", streamName, err)
		}
		log.Infof("stream info: %v", info)
	}

	return &EventStore{
		js:           js,
		saveEventsFn: saveEventsFn,
		getEventsFn:  getEventsFn,
	}
}

func getTagsAsMap(criteria *[]*Tag, eventType string) map[string]interface{} {
	result := make(map[string]interface{}, len(*criteria))

	for _, criterion := range *criteria {
		result[criterion.Key] = criterion.Value
	}
	result["eventType"] = eventType
	return result
}

type EventWithMapTags struct {
	EventId   string                 `json:"event_id"`
	EventType string                 `json:"event_type"`
	Data      interface{}            `json:"data"`
	Metadata  interface{}            `json:"metadata"`
	Tags      map[string]interface{} `json:"tags"`
}

func (s *EventStore) SaveEvents(ctx context.Context, req *SaveEventsRequest) (resp *WriteResult, err error) {
	logger.Debugf("SaveEvents called with req: %v", req)
	// Defer a recovery function to catch any panics
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("Panic in SaveEvents: %v\nStack Trace:\n%s", r, debug.Stack())
			err = status.Errorf(codes.Internal, "Internal server error")
		}
	}()

	// Validate the request
	if req == nil || req.ConsistencyCondition == nil || len(req.Events) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Invalid request: missing consistency condition or events")
	}

	eventsForMarshaling := make([]EventWithMapTags, len(req.Events))
	for i, event := range req.Events {
		var dataMap, metadataMap map[string]interface{}

		if err := json.Unmarshal([]byte(event.Data), &dataMap); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "Invalid JSON in data field: %v", err)
		}

		if err := json.Unmarshal([]byte(event.Metadata), &metadataMap); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "Invalid JSON in metadata field: %v", err)
		}

		eventsForMarshaling[i] = EventWithMapTags{
			EventId:   event.EventId,
			EventType: event.EventType,
			Data:      dataMap,
			Metadata:  metadataMap,
			Tags:      getTagsAsMap(&event.Tags, event.EventType),
		}
	}
	eventsJSON, err := json.Marshal(eventsForMarshaling)
	if err != nil {
		logger.Errorf("Error marshaling events: %v", err)
		return nil, status.Errorf(codes.Internal, "Failed to marshal events")
	}
	logger.Debugf("eventsJSON: %v", string(eventsJSON))

	var transactionID string
	var globalID int64

	// Execute the query
	transactionID, globalID, err = s.saveEventsFn.Save(ctx, &eventsForMarshaling, req.ConsistencyCondition, req.Boundary)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to save events: %v", err)
	}

	return &WriteResult{
		LogPosition: &Position{
			CommitPosition:  parseInt64(transactionID),
			PreparePosition: globalID,
		},
	}, nil
}

func (s *EventStore) GetEvents(ctx context.Context, req *GetEventsRequest) (*GetEventsResponse, error) {
	if req.LastRetrievedPosition == nil || req.Count == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "LastRetrievedPosition and Count are required")
	}

	return s.getEventsFn.Get(ctx, req)
}

func (s *EventStore) CatchUpSubscribeToEvents(req *CatchUpSubscribeToEventStoreRequest, stream EventStore_CatchUpSubscribeToEventsServer) error {
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

	// Create or access the KV store for active subscriptions
	kv, err := s.js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket:      activeSubscriptionsKVBucket,
		Description: "Active subscriptions",
		Storage:     jetstream.MemoryStorage,
	})

	if err != nil {
		return status.Errorf(codes.Internal, "failed to access KV store: %v", err)
	}

	// Try to create a new entry for this subscription
	keyName := req.Boundary + "." + req.SubscriberName
	_, err = kv.Put(ctx, keyName, []byte(time.Now().String()))
	if err != nil {
		if strings.Contains(err.Error(), "key exists") {
			return status.Errorf(codes.AlreadyExists, "subscription already exists for subject: %s", req.SubscriberName)
		}
		return status.Errorf(codes.Internal, "failed to create subscription entry: %v", err)
	}

	// Ensure we remove the KV entry when we're done
	defer kv.Delete(ctx, keyName)

	// Initialize position tracking
	var positionMu sync.RWMutex
	lastPosition := req.Position
	if lastPosition == nil {
		lastPosition = &Position{CommitPosition: 0, PreparePosition: 0}
	}

	// Process historical events
	historicalDone := make(chan struct{})
	var historicalErr error
	go func() {
		defer close(historicalDone)
		var lastTime time.Time
		lastPosition, lastTime, historicalErr = s.sendHistoricalEvents(ctx, lastPosition, req.Query, stream, req.Boundary)
		if historicalErr != nil {
			logger.Errorf("Historical events processing failed: %v", historicalErr)
			return
		}
		logger.Infof("Historical events processed up to %v", lastTime)
	}()

	// Wait for historical processing
	select {
	case <-historicalDone:
		if historicalErr != nil {
			return status.Errorf(codes.Internal, "historical events failed: %v", historicalErr)
		}
	case <-ctx.Done():
		return ctx.Err()
	}

	// Set up NATS subscription for live events
	subs, err := s.js.Stream(ctx, GetEventsStreamName(req.Boundary))
	if err != nil {
		return status.Errorf(codes.Internal, "failed to get stream: %v", err)
	}

	consumer, err := subs.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name: req.SubscriberName,
		// FilterSubject:  GetEventsSubjectName(req.Boundary),
		DeliverPolicy: jetstream.DeliverNewPolicy,
		AckPolicy:     jetstream.AckExplicitPolicy,
		MaxDeliver:    -1,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
		MaxAckPending: 100,
	})
	if err != nil {
		return status.Errorf(codes.Internal, "failed to create consumer: %v", err)
	}
	defer subs.DeleteConsumer(ctx, req.SubscriberName)

	// Start consuming messages
	msgs, err := consumer.Messages()
	if err != nil {
		return status.Errorf(codes.Internal, "failed to get message iterator: %v", err)
	}

	// Keep the connection alive and process new messages
	for {
		select {
		case <-ctx.Done():
			logger.Info("Context cancelled, stopping subscription")
			return ctx.Err()
		default:
			msg, err := msgs.Next()
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				logger.Errorf("Error getting next message: %v", err)
				continue
			}

			var event Event
			if err := json.Unmarshal(msg.Data(), &event); err != nil {
				logger.Errorf("Failed to unmarshal event: %v", err)
				msg.Ack()
				continue
			}

			positionMu.RLock()
			isNewer := isEventNewer(event.Position, lastPosition)
			positionMu.RUnlock()

			if isNewer && s.eventMatchesQueryCriteria(&event, req.Query) {
				if err := stream.Send(&event); err != nil {
					logger.Errorf("Failed to send event: %v", err)
					msg.Nak() // Negative acknowledgment to retry later
					continue
				}

				positionMu.Lock()
				lastPosition = event.Position
				positionMu.Unlock()

				if err := msg.Ack(); err != nil {
					logger.Errorf("Failed to acknowledge message: %v", err)
				}
			} else {
				msg.Ack() // Acknowledge messages that don't match criteria
			}
		}
	}
}

// isEventNewer checks if the new event position is greater than the last processed position
func isEventNewer(newPosition, lastPosition *Position) bool {
	if newPosition.CommitPosition > lastPosition.CommitPosition {
		return true
	}
	if newPosition.CommitPosition == lastPosition.CommitPosition {
		return newPosition.PreparePosition > lastPosition.PreparePosition
	}
	return false
}

func (s *EventStore) sendHistoricalEvents(ctx context.Context, fromPosition *Position, query *Query, stream EventStore_CatchUpSubscribeToEventsServer, boundary string) (*Position, time.Time, error) {
	lastPosition := fromPosition
	var lastEventTime time.Time
	batchSize := int32(100) // Adjust as needed

	for {
		events, err := s.GetEvents(ctx, &GetEventsRequest{
			Query:                 query,
			LastRetrievedPosition: lastPosition,
			Count:                 batchSize,
			Direction:             Direction_ASC,
			Boundary:              boundary,
		})
		if err != nil {
			return nil, time.Time{}, status.Errorf(codes.Internal, "failed to fetch historical events: %v", err)
		}

		for _, event := range events.Events {
			if err := stream.Send(event); err != nil {
				return nil, time.Time{}, err
			}
			lastPosition = event.Position
			lastEventTime = event.DateCreated.AsTime()
		}

		if len(events.Events) < int(batchSize) {
			// We've reached the end of historical events
			break
		}
	}

	return lastPosition, lastEventTime, nil
}

func (s *EventStore) eventMatchesQueryCriteria(event *Event, criteria *Query) bool {
	if criteria == nil || len(criteria.Criteria) == 0 {
		return true
	}

	// For multiple criteria groups, ANY group matching is sufficient (OR logic)
	for _, criteriaGroup := range criteria.Criteria {
		allTagsMatch := true

		// Within a group, ALL tags must match (AND logic)
		for _, criteriaTag := range criteriaGroup.Tags {
			tagFound := false
			for _, eventTag := range event.Tags {
				if eventTag.Key == criteriaTag.Key && eventTag.Value == criteriaTag.Value {
					tagFound = true
					break
				}
			}
			if !tagFound {
				allTagsMatch = false
				break
			}
		}

		// If all tags in this group matched, we can return true
		if allTagsMatch {
			return true
		}
	}

	// No criteria group fully matched
	return false
}

func getPubSubStreamName(subjectName string) string {
	return pubsubPrefix + subjectName
}

func (s *EventStore) SubscribeToPubSub(req *SubscribeRequest, stream EventStore_SubscribeToPubSubServer) error {
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

	logger.Infof("SubscribeToPubSub called with subject: %s, consumer_name: %s", req.Subject, req.ConsumerName)

	pubSubStreamName := getPubSubStreamName(req.Subject)
	natsStream, err := s.js.Stream(ctx, pubSubStreamName)

	if err != nil && err.Error() != jetstream.ErrStreamNotFound.Error() {
		return status.Errorf(codes.Internal, "failed to subscribe: %v", err)
	}

	if natsStream == nil {
		natsStream, err = s.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
			Name:              pubSubStreamName,
			Subjects:          []string{pubSubStreamName + ".*"},
			Storage:           jetstream.MemoryStorage,
			MaxConsumers:      -1, // Allow unlimited consumers
			MaxAge:            24 * time.Hour,
			MaxMsgsPerSubject: 1000,
		})
		if err != nil {
			return status.Errorf(codes.Internal, "failed to add stream: %v", err)
		}
		logger.Debugf("stream info: %v", natsStream)
	}

	sub, err := natsStream.CreateOrUpdateConsumer(
		ctx,
		jetstream.ConsumerConfig{
			Name: req.ConsumerName,
			// FilterSubject: pubSubStreamName + "." + req.Subject,
			DeliverPolicy: jetstream.DeliverNewPolicy,
			AckPolicy:     jetstream.AckExplicitPolicy,
			MaxAckPending: 100,
		},
	)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to subscribe: %v", err)
	}
	defer s.js.DeleteConsumer(ctx, req.ConsumerName, pubsubPrefix+req.Subject)

	_, err = sub.Consume(func(msg jetstream.Msg) {
		for {
			err := stream.Send(&SubscribeResponse{
				Message: &Message{
					Id:      msg.Headers().Get("Nats-Msg-Id"),
					Subject: msg.Subject(),
					Data:    msg.Data(),
				},
			})

			if err == nil {
				// Message sent successfully, break the retry loop
				msg.Ack()
				break
			}
			if stream.Context().Err() != nil {
				// Client has disconnected, exit the handler
				logger.Infof("Client disconnected: %v", stream.Context().Err())
				return
			}
			// Log the error and retry
			logger.Errorf("Error sending message to gRPC stream: %v. Retrying...", err)
			// Optional: add a short delay before retrying
			time.Sleep(time.Millisecond * 100)
		}
	})

	if err != nil {
		return status.Errorf(codes.Internal, "failed to subscribe: %v", err)
	}

	<-stream.Context().Done()
	return stream.Context().Err()
}

func parseInt64(s string) int64 {
	var i int64
	fmt.Sscanf(s, "%d", &i)
	return i
}

func (s *EventStore) PublishToPubSub(ctx context.Context, req *PublishRequest) (*emptypb.Empty, error) {
	msgJSON, err := json.Marshal(req)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to marshal message: %v", err)
	}

	_, err = s.js.Publish(ctx, getPubSubStreamName(req.Subject)+"."+req.Subject, msgJSON)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to publish message: %v", err)
	}

	return &emptypb.Empty{}, nil
}

func GetLastPublishedPosition(ctx context.Context, js jetstream.JetStream, boundary string) (*Position, error) {
	eventsStreamName := GetEventsStreamName(boundary)
	stream, err := js.Stream(ctx, eventsStreamName)
	if err != nil {
		return nil, err
	}
	logger.Debugf("stream info: %v", stream)

	info, err := stream.Info(ctx)
	if err != nil {
		return nil, err
	}
	if info.State.LastSeq == 0 {
		return &Position{CommitPosition: 0, PreparePosition: 0}, nil
	}

	msg, err := stream.GetMsg(ctx, info.State.LastSeq)
	if err != nil {
		return nil, err
	}

	var event Event
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return nil, err
	}

	return event.Position, nil
}
