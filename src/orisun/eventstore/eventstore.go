package eventstore

import (
	"context"
	"encoding/json"
	"fmt"
	reflect "reflect"
	"runtime/debug"
	sync "sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"

	"strings"

	"github.com/nats-io/nats.go"
	logging "orisun/src/orisun/logging"
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
	js           nats.JetStreamContext
	saveEventsFn SaveEvents
	getEventsFn  GetEvents
}

const (
	eventsStreamPrefix          = "$ORISUN_EVENTS"
	EventsSubjectName           = "EVENTS"
	pubsubPrefix                = "orisun_pubsub."
	pubsubStreamName            = "ORISUN_PUB_SUB"
	activeSubscriptionsKVBucket = "ACTIVE_SUBSCRIPTIONS"
)

var logger logging.Logger

func NewPostgresEventStoreServer(js nats.JetStreamContext,
	saveEventsFn SaveEvents,
	getEventsFn GetEvents,
	boundaries []string) *EventStore {
	log, err := logging.GlobalLogger()

	if err != nil {
		log.Fatalf("Could not configure logger")
	}

	logger = log
	for _, boundary := range boundaries {
		existingStreamInfo, _ := js.StreamInfo(GetEventsStreamName(eventsStreamPrefix, boundary))
		
		if existingStreamInfo == nil {
			info, err := js.AddStream(&nats.StreamConfig{
				Name: GetEventsStreamName(eventsStreamPrefix, boundary),
				Subjects: []string{
					eventsStreamPrefix + "." + boundary + "." + EventsSubjectName,
				},
				MaxAge: 24 * time.Hour,
			})
			if err != nil {
				log.Fatalf("failed to add stream: %v", err)
			}
			log.Infof("stream info: %v", info)
		}
	}

	existingPubsubStreamInfo, err := js.StreamInfo(pubsubStreamName)
	if err != nil {
		log.Fatalf("failed to get stream info: %v", err)
	}
	if existingPubsubStreamInfo == nil {
		info, errr := js.AddStream(&nats.StreamConfig{
			Name:         pubsubStreamName,
			Subjects:     []string{pubsubPrefix + ">"},
			Storage:      nats.FileStorage,
			MaxConsumers: -1, // Allow unlimited consumers
			MaxAge:       24 * time.Hour,
		})
		if errr != nil {
			log.Fatalf("failed to add stream: %v", errr)
		}
		log.Infof("stream info: %v", info)
	}

	return &EventStore{
		js:           js,
		saveEventsFn: saveEventsFn,
		getEventsFn:  getEventsFn,
	}
}

func getTagsAsMap(criteria *[]*Tag) map[string]interface{} {
	result := make(map[string]interface{}, len(*criteria))

	for _, criterion := range *criteria {
		result[criterion.Key] = criterion.Value
	}
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
			Tags:      getTagsAsMap(&event.Tags),
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

func (s *EventStore) SubscribeToEvents(req *SubscribeToEventStoreRequest, stream EventStore_SubscribeToEventsServer) error {
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel() // Ensure all resources are cleaned up when we exit

	// Create or access the KV store for active subscriptions
	kv, err := s.js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:      activeSubscriptionsKVBucket,
		Description: "Active subscriptions",
		Storage:     nats.MemoryStorage,
	})

	if err != nil {
		return status.Errorf(codes.Internal, "failed to access KV store: %v", err)
	}

	// Try to create a new entry for this subscription
	_, err = kv.Create(req.SubscriberName, []byte(time.Now().String()))
	if err != nil {
		if strings.Contains(err.Error(), "key exists") {
			return status.Errorf(codes.AlreadyExists, "subscription already exists for subject: %s", req.SubscriberName)
		}
		return status.Errorf(codes.Internal, "failed to create subscription entry: %v", err)
	}

	// Ensure we remove the KV entry when we're done
	defer kv.Delete(req.SubscriberName)

	// Set up initial position
	lastPosition := req.Position
	if lastPosition == nil {
		lastPosition = &Position{CommitPosition: 0, PreparePosition: 0}
	}

	// Create a channel to signal when historical events are done
	historicalDone := make(chan struct{})

	// Start sending historical events in a separate goroutine
	var lastHistoricalPosition *Position
	var lastHistoricalTime time.Time
	var historicalErr error
	go func() {
		defer close(historicalDone)
		lastHistoricalPosition, lastHistoricalTime, historicalErr = s.sendHistoricalEvents(ctx, lastPosition, req.Criteria, stream)
	}()

	// Wait for historical events to finish or context to be cancelled
	select {
	case <-historicalDone:
		if historicalErr != nil {
			return historicalErr
		}
	case <-ctx.Done():
		return ctx.Err()
	}

	// Set up NATS subscription
	sub, err := s.js.SubscribeSync(EventsSubjectName, nats.StartTime(lastHistoricalTime.Add(time.Nanosecond)))
	if err != nil {
		return status.Errorf(codes.Internal, "failed to subscribe to events: %v", err)
	}
	defer sub.Unsubscribe()

	// Use a WaitGroup to ensure all goroutines are done before returning
	var wg sync.WaitGroup
	wg.Add(1)

	// Start processing NATS messages in a separate goroutine
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				msg, err := sub.NextMsg(time.Second)
				if err == nats.ErrTimeout {
					continue
				}
				if err != nil {
					if ctx.Err() == nil {
						// Only log if the context hasn't been cancelled
						logger.Errorf("Error receiving message: %v", err)
					}
					return
				}

				var event Event
				if err := json.Unmarshal(msg.Data, &event); err != nil {
					logger.Errorf("Failed to unmarshal event: %v", err)
					continue
				}

				if isEventNewer(event.Position, lastHistoricalPosition) {
					if s.eventMatchesCriteria(&event, req.Criteria) {
						if err := stream.Send(&event); err != nil {
							if ctx.Err() == nil {
								logger.Errorf("Error sending event: %v", err)
							}
							return
						}
					}
					lastHistoricalPosition = event.Position
				}
			}
		}
	}()

	// Wait for the context to be done (client disconnected or cancelled)
	<-ctx.Done()

	// Wait for all goroutines to finish
	wg.Wait()

	return ctx.Err()
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

func (s *EventStore) sendHistoricalEvents(ctx context.Context, fromPosition *Position, criteria *Criteria, stream EventStore_SubscribeToEventsServer) (*Position, time.Time, error) {
	lastPosition := fromPosition
	var lastEventTime time.Time
	batchSize := int32(100) // Adjust as needed

	for {
		events, err := s.GetEvents(ctx, &GetEventsRequest{
			Criteria:              criteria,
			LastRetrievedPosition: lastPosition,
			Count:                 batchSize,
			Direction:             Direction_ASC,
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

func (s *EventStore) eventMatchesCriteria(event *Event, criteria *Criteria) bool {
	if len(criteria.Criteria) == 0 {
		return true
	}
	for i := 0; i < len(criteria.Criteria); i++ {
		// Check if both tags are not nil and match
		if criteria.Criteria[i].Tags != nil && event.Tags != nil && reflect.DeepEqual(criteria.Criteria[i].Tags, event.Tags) {
			return true
		}
	}
	return false
}

func (s *EventStore) SubscribeToPubSub(req *SubscribeRequest, stream EventStore_SubscribeToPubSubServer) error {
	logger.Infof("SubscribeToPubSub called with subject: %s, consumer_name: %s", req.Subject, req.ConsumerName)

	sub, err := s.js.QueueSubscribe(
		pubsubPrefix+req.Subject,
		req.ConsumerName,
		func(msg *nats.Msg) {
			for {
				err := stream.Send(&SubscribeResponse{
					Message: &Message{
						Id:      msg.Header.Get("Nats-Msg-Id"),
						Subject: msg.Subject,
						Data:    msg.Data,
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
		},
		nats.Durable(req.ConsumerName),
		nats.AckExplicit(),
		nats.DeliverAll(),
		nats.BindStream(pubsubStreamName),
	)

	if err != nil {
		return status.Errorf(codes.Internal, "failed to subscribe: %v", err)
	}
	defer sub.Unsubscribe()

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

	_, err = s.js.Publish(pubsubPrefix+req.Subject, msgJSON, nats.MsgId(req.Id))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to publish message: %v", err)
	}

	return &emptypb.Empty{}, nil
}

func GetLastPublishedPosition(js nats.JetStreamContext, boundary string) (*Position, error) {
	eventsStreamName := GetEventsStreamName(eventsStreamPrefix, boundary)
	info, err := js.StreamInfo(eventsStreamName)
	if err != nil {
		return nil, err
	}
	logger.Debugf("stream info: %v", info)

	if info.State.LastSeq == 0 {
		return &Position{CommitPosition: 0, PreparePosition: 0}, nil
	}

	msg, err := js.GetMsg(eventsStreamName, info.State.LastSeq)
	if err != nil {
		return nil, err
	}

	var event Event
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return nil, err
	}

	return event.Position, nil
}

func GetEventsStreamName(eventsStreamPrefix string, boundary string) string {
	return eventsStreamPrefix + "_" + boundary
}
