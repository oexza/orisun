package eventstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	reflect "reflect"
	"runtime/debug"
	sync "sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"strings"

	"github.com/nats-io/nats.go"
	logging "orisun/src/orisun/logging"
)

type PostgresEventStoreServer struct {
	UnimplementedEventStoreServer
	db *sql.DB
	js nats.JetStreamContext
}

const (
	advisoryLockID              = 12345
	eventsStreamName            = "$ORISUN_EVENTS"
	eventsSubjectName           = "EVENTS"
	pubsubPrefix                = "orisun_pubsub."
	pubsubStreamName            = "ORISUN_PUB_SUB"
	activeSubscriptionsKVBucket = "ACTIVE_SUBSCRIPTIONS"
)

var logger logging.Logger

func NewPostgresEventStoreServer(db *sql.DB, js nats.JetStreamContext) *PostgresEventStoreServer {
	log, err := logging.GlobalLogger()

	if err != nil {
		log.Fatalf("Could not configure logger")
	}

	logger = log
	info, err := js.AddStream(&nats.StreamConfig{
		Name: eventsStreamName,
		Subjects: []string{
			eventsSubjectName,
		},
	})
	if err != nil {
		log.Fatalf("failed to add stream: %v", err)
	}
	log.Infof("stream info: %v", info)

	info, err = js.AddStream(&nats.StreamConfig{
		Name:         pubsubStreamName,
		Subjects:     []string{pubsubPrefix + ">"},
		Storage:      nats.FileStorage,
		MaxConsumers: -1, // Allow unlimited consumers
	})
	if err != nil {
		log.Fatalf("failed to add stream: %v", err)
	}
	log.Infof("stream info: %v", info)

	return &PostgresEventStoreServer{
		db: db,
		js: js,
	}
}

const insertEventsWithConsistency = `
SELECT * FROM insert_events_with_consistency($1::jsonb, $2::jsonb)
`

func getCriteriaAsList(criteria *Criteria) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(criteria.Criteria))
	for _, criterion := range criteria.Criteria {
		anded := make(map[string]interface{}, len(criterion.Tags))
		for _, tag := range criterion.Tags {
			anded[tag.Key] = tag.Value
		}
		result = append(result, anded)
	}
	return result
}

func getTagsAsMap(criteria *[]*Tag) map[string]interface{} {
	result := make(map[string]interface{}, len(*criteria))

	for _, criterion := range *criteria {
		result[criterion.Key] = criterion.Value
	}
	return result
}

func getConsistencyConditionAsMap(consistencyCondition *ConsistencyCondition) map[string]interface{} {
	lastRetrievedPositions := make(map[string]int64)
	if consistencyCondition.ConsistencyMarker != nil {
		lastRetrievedPositions["transaction_id"] = consistencyCondition.ConsistencyMarker.CommitPosition
		lastRetrievedPositions["global_id"] = consistencyCondition.ConsistencyMarker.PreparePosition
	}

	criteriaList := getCriteriaAsList(consistencyCondition.Criteria)

	return map[string]interface{}{
		"last_retrieved_position": lastRetrievedPositions,
		"criteria":                criteriaList,
	}
}

type eventWithMapTags struct {
	EventId   string                 `json:"event_id"`
	EventType string                 `json:"event_type"`
	Data      interface{}            `json:"data"`
	Metadata  interface{}            `json:"metadata"`
	Tags      map[string]interface{} `json:"tags"`
}

func (s *PostgresEventStoreServer) SaveEvents(ctx context.Context, req *SaveEventsRequest) (resp *WriteResult, err error) {
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

	consistencyConditionMap := getConsistencyConditionAsMap(req.ConsistencyCondition)
	consistencyConditionJSON, err := json.Marshal(consistencyConditionMap)
	if err != nil {
		logger.Errorf("Error marshaling consistency condition: %v", err)
		return nil, status.Errorf(codes.Internal, "Failed to marshal consistency condition")
	}

	eventsForMarshaling := make([]eventWithMapTags, len(req.Events))
	for i, event := range req.Events {
		var dataMap, metadataMap map[string]interface{}

		if err := json.Unmarshal([]byte(event.Data), &dataMap); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "Invalid JSON in data field: %v", err)
		}

		if err := json.Unmarshal([]byte(event.Metadata), &metadataMap); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "Invalid JSON in metadata field: %v", err)
		}

		eventsForMarshaling[i] = eventWithMapTags{
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
	row := s.db.QueryRowContext(ctx, insertEventsWithConsistency, string(consistencyConditionJSON), string(eventsJSON))
	logger.Debugf("row: %v", row)
	// Scan the result
	noop := false
	err = row.Scan(&transactionID, &globalID, &noop)
	if err != nil {
		if strings.Contains(err.Error(), "OptimisticConcurrencyException") {
			return nil, status.Errorf(codes.AlreadyExists, err.Error())
		}
		logger.Errorf("Error saving events to database: %v", err)
		return nil, status.Errorf(codes.Internal, "Error saving events to database")
	}

	return &WriteResult{
		LogPosition: &Position{
			CommitPosition:  parseInt64(transactionID),
			PreparePosition: globalID,
		},
	}, nil
}

const selectMatchingEvents = `
SELECT * FROM get_matching_events($1::jsonb, $2, $3)
`

func (s *PostgresEventStoreServer) GetEvents(ctx context.Context, req *GetEventsRequest) (*GetEventsResponse, error) {
	if req.LastRetrievedPosition == nil || req.Count == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "LastRetrievedPosition and Count are required")
	}

	var criteriaList []map[string]interface{}
	if req.Criteria != nil {
		criteriaList = getCriteriaAsList(req.Criteria)
	}

	fromPosition := map[string]int64{
		"transaction_id": req.LastRetrievedPosition.CommitPosition,
		"global_id":      req.LastRetrievedPosition.PreparePosition,
	}

	params := map[string]interface{}{
		"last_retrieved_position": fromPosition,
	}

	if len(criteriaList) > 0 {
		params["criteria"] = criteriaList
	}

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to marshal params: %v", err)
	}
	logger.Debugf("params: %v", string(paramsJSON))
	logger.Debugf("direction: %v", req.Direction.String())
	logger.Debugf("count: %v", req.Count)

	rows, err := s.db.QueryContext(ctx, selectMatchingEvents, string(paramsJSON), req.Direction.String(), req.Count)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to execute query: %v", err)
	}
	defer rows.Close()

	var events []*Event

	for rows.Next() {
		var event Event
		var tagsBytes []byte
		var transactionID, globalID int64
		var dateCreated time.Time

		// Create a map of pointers to hold our row data
		rowData := map[string]interface{}{
			"event_id":       &event.EventId,
			"event_type":     &event.EventType,
			"data":           &event.Data,
			"metadata":       &event.Metadata,
			"tags":           &tagsBytes,
			"transaction_id": &transactionID,
			"global_id":      &globalID,
			"date_created":   &dateCreated,
		}

		// Get the column names from the result set
		columns, err := rows.Columns()
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to get column names: %v", err)
		}

		// Create a slice of pointers to scan into
		scanArgs := make([]interface{}, len(columns))
		for i, col := range columns {
			if ptr, ok := rowData[col]; ok {
				scanArgs[i] = ptr
			} else {
				return nil, status.Errorf(codes.Internal, "unexpected column: %s", col)
			}
		}

		// Scan the row into our map
		if err := rows.Scan(scanArgs...); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to scan row: %v", err)
		}

		// Process tags
		var tagsMap map[string]string
		if err := json.Unmarshal(tagsBytes, &tagsMap); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to unmarshal tags: %v", err)
		}
		for key, value := range tagsMap {
			event.Tags = append(event.Tags, &Tag{Key: key, Value: value})
		}

		// Set the Position
		event.Position = &Position{
			CommitPosition:  transactionID,
			PreparePosition: globalID,
		}

		// Set the DateCreated
		event.DateCreated = timestamppb.New(dateCreated)

		events = append(events, &event)
	}

	if err := rows.Err(); err != nil {
		return nil, status.Errorf(codes.Internal, "error iterating rows: %v", err)
	}

	return &GetEventsResponse{Events: events}, nil
}

func (s *PostgresEventStoreServer) SubscribeToEvents(req *SubscribeToEventStoreRequest, stream EventStore_SubscribeToEventsServer) error {
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
	sub, err := s.js.SubscribeSync(eventsSubjectName, nats.StartTime(lastHistoricalTime.Add(time.Nanosecond)))
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

func (s *PostgresEventStoreServer) sendHistoricalEvents(ctx context.Context, fromPosition *Position, criteria *Criteria, stream EventStore_SubscribeToEventsServer) (*Position, time.Time, error) {
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

func (s *PostgresEventStoreServer) eventMatchesCriteria(event *Event, criteria *Criteria) bool {
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

func (s *PostgresEventStoreServer) SubscribeToPubSub(req *SubscribeRequest, stream EventStore_SubscribeToPubSubServer) error {
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

func PollEventsFromPgToNats(ctx context.Context, db *sql.DB, js nats.JetStreamContext, eventStore *PostgresEventStoreServer, batchSize int32) {
	for {
		select {
		case <-ctx.Done():
			logger.Infof("Polling stopped: %v", ctx.Err())
			return
		default:
			var gotLock bool
			dberr := db.QueryRow("SELECT pg_try_advisory_lock($1)", advisoryLockID).Scan(&gotLock)
			if dberr != nil {
				logger.Errorf("failed to acquire advisory lock: %v", dberr)
				time.Sleep(1 * time.Second) // Retry interval
				continue
			}
			if !gotLock {
				time.Sleep(1 * time.Second) // Retry interval if lock not acquired
				continue
			}
			defer db.Exec("SELECT pg_advisory_unlock($1)", advisoryLockID) // Ensure lock is released

			lastPosition, err := getLastPublishedPosition(js)
			if err != nil {
				logger.Errorf("failed to get last published position: %v", err)
				time.Sleep(2 * time.Second) // Retry interval
				continue
			}

			req := &GetEventsRequest{
				LastRetrievedPosition: lastPosition,
				Count:                 batchSize,
				Direction:             Direction_ASC,
			}
			resp, err := eventStore.GetEvents(context.Background(), req)
			if err != nil {
				logger.Errorf("failed to get events: %v", err)
				time.Sleep(1 * time.Second) // Retry interval
				continue
			}

			for _, event := range resp.Events {
				eventData, err := json.Marshal(event)
				if err != nil {
					logger.Errorf("failed to marshal event: %v", err)
					continue
				}
				publishEventWithRetry(js, eventData)
			}
		}
		time.Sleep(1 * time.Second) // Polling interval
	}
}

func publishEventWithRetry(js nats.JetStreamContext, eventData []byte) {
	backoff := time.Second
	maxBackoff := time.Minute * 5
	attempt := 1

	for {
		_, err := js.Publish(eventsSubjectName, eventData)
		if err == nil {
			logger.Debugf("Successfully published event after %d attempts", attempt)
			return
		}

		logger.Errorf("Failed to publish event (attempt %d): %v", attempt, err)

		// Calculate next backoff duration
		nextBackoff := backoff * 2
		if nextBackoff > maxBackoff {
			nextBackoff = maxBackoff
		}

		logger.Infof("Retrying in %v", backoff)
		time.Sleep(backoff)

		backoff = nextBackoff
		attempt++
	}
}

func parseInt64(s string) int64 {
	var i int64
	fmt.Sscanf(s, "%d", &i)
	return i
}

func (s *PostgresEventStoreServer) PublishToPubSub(ctx context.Context, req *PublishRequest) (*emptypb.Empty, error) {
	msgJSON, err := json.Marshal(req)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to marshal message: %v", err)
	}

	_, err = s.js.Publish(pubsubPrefix+req.Subject, msgJSON)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to publish message: %v", err)
	}

	return &emptypb.Empty{}, nil
}

func getLastPublishedPosition(js nats.JetStreamContext) (*Position, error) {
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
