package postgres_eventstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"orisun/src/orisun/logging"
	"strings"
	"time"

	eventstore "orisun/src/orisun/eventstore"

	config "orisun/src/orisun/config"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	advisoryLockID = 12345
)

const insertEventsWithConsistency = `
SELECT * FROM %s.insert_events_with_consistency($1::jsonb, $2::jsonb, $3::jsonb)
`

const selectMatchingEvents = `
SELECT * FROM get_matching_events($1, $2::jsonb, $3::jsonb, $4, $5)
`

const setSearchPath = `
set search_path to '%s'
`

type PostgresSaveEvents struct {
	db                     *sql.DB
	logger                 logging.Logger
	boundarySchemaMappings map[string]config.BoundaryToPostgresSchemaMapping
}

func NewPostgresSaveEvents(db *sql.DB, logger *logging.Logger,
	boundarySchemaMappings map[string]config.BoundaryToPostgresSchemaMapping) *PostgresSaveEvents {
	return &PostgresSaveEvents{db: db, logger: *logger, boundarySchemaMappings: boundarySchemaMappings}
}

type PostgresGetEvents struct {
	db                     *sql.DB
	logger                 logging.Logger
	boundarySchemaMappings map[string]config.BoundaryToPostgresSchemaMapping
}

func NewPostgresGetEvents(db *sql.DB, logger *logging.Logger,
	boundarySchemaMappings map[string]config.BoundaryToPostgresSchemaMapping) *PostgresGetEvents {
	return &PostgresGetEvents{db: db, logger: *logger, boundarySchemaMappings: boundarySchemaMappings}
}

func (s *PostgresSaveEvents) Save(
	ctx context.Context,
	events *[]eventstore.EventWithMapTags,
	consistencyCondition *eventstore.IndexLockCondition,
	boundary string,
	streamName string,
	expectedVersion uint32,
	streamConsistencyCondition *eventstore.Query) (transactionID string, globalID uint64, err error) {
	var streamSubsetQueryJSON *string

	streamSubsetAsJsonString, err := json.Marshal(getStreamSectionAsMap(streamName, expectedVersion, streamConsistencyCondition))
	if err != nil {
		return "", 0, status.Errorf(codes.Internal, "failed to marshal consistency condition: %v", err)
	}
	jsonStr := string(streamSubsetAsJsonString)
	s.logger.Debugf("streamSubsetAsJsonString: %v", jsonStr)
	streamSubsetQueryJSON = &jsonStr

	var consistencyConditionJSONString *string = nil
	if consistencyCondition != nil {
		consistencyConditionJSON, err := json.Marshal(getConsistencyConditionAsMap(consistencyCondition))
		if err != nil {
			return "", 0, status.Errorf(codes.Internal, "failed to marshal consistency condition: %v", err)
		}
		jsonStr := string(consistencyConditionJSON)
		consistencyConditionJSONString = &jsonStr
	}

	eventsJSON, err := json.Marshal(events)
	if err != nil {
		return "", 0, status.Errorf(codes.Internal, "failed to marshal events: %v", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, status.Errorf(codes.Internal, "failed to begin transaction: %v", err)
	}
	defer tx.Rollback()

	var schema = s.boundarySchemaMappings[boundary].Schema

	_, err = tx.ExecContext(ctx, fmt.Sprintf(setSearchPath, schema))
	if err != nil {
		return "", 0, status.Errorf(codes.Internal, "failed to set search path: %v", err)
	}

	s.logger.Debugf("insertEventsWithConsistency: %s", &streamSubsetQueryJSON)
	row := tx.QueryRowContext(
		ctx,
		fmt.Sprintf(insertEventsWithConsistency, schema),
		streamSubsetQueryJSON,
		consistencyConditionJSONString,
		string(eventsJSON),
	)

	if row.Err() != nil {
		return "", 0, status.Errorf(codes.Internal, "failed to insert events: %v", row.Err())
	}

	// Scan the result
	noop := false
	err = error(nil)

	var tranID string
	var globID uint64
	err = row.Scan(&tranID, &globID, &noop)
	err = tx.Commit()

	if err != nil {
		return "", 0, status.Errorf(codes.Internal, "failed to commit transaction: %v", err)
	}

	if err != nil {
		if strings.Contains(err.Error(), "OptimisticConcurrencyException") {
			return "", 0, status.Errorf(codes.AlreadyExists, err.Error())
		}
		s.logger.Errorf("Error saving events to database: %v", err)
		return "", 0, status.Errorf(codes.Internal, "Error saving events to database")
	}

	return tranID, globID, nil
}

func (s *PostgresGetEvents) Get(ctx context.Context, req *eventstore.GetEventsRequest) (*eventstore.GetEventsResponse, error) {
	var fromPosition *map[string]uint64

	if req.FromPosition != nil && req.FromPosition != (&eventstore.Position{}) {
		fromPosition = &map[string]uint64{
			"transaction_id": req.FromPosition.CommitPosition,
			"global_id":      req.FromPosition.PreparePosition,
		}
	}

	var globalQuery *(map[string]interface{})

	var criteriaList []map[string]interface{}
	if req.Query != nil {
		criteriaList = getCriteriaAsList(req.Query)
	}

	if len(criteriaList) > 0 {
		globalQuery = &map[string]interface{}{
			"criteria": criteriaList,
		}
	}

	var paramsJSON *string = nil

	if globalQuery != nil && len(*globalQuery) > 0 {
		var err interface{} = ""
		paramsString, err := json.Marshal(globalQuery)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to marshal params: %v", err)
		}
		stringJson := string(paramsString)
		paramsJSON = &stringJson
	}

	var streamName *string = nil
	if req.Stream != nil {
		streamName = &req.Stream.Name
	} else {
		streamName = nil
	}

	s.logger.Debugf("params: %v", paramsJSON)
	s.logger.Debugf("direction: %v", req.Direction.String())
	s.logger.Debugf("count: %v", req.Count)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to begin transaction: %v", err)
	}
	defer tx.Rollback()

	var schema = s.boundarySchemaMappings[req.Boundary].Schema

	_, err = tx.ExecContext(ctx, fmt.Sprintf(setSearchPath, schema))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to set search path: %v", err)
	}

	fromPositionMarshaled, err := json.Marshal(fromPosition)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to marshal from position: %v", err)
	}

	exec, err := tx.Exec("SET log_statement = 'all';")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to set log_statement: %v", err)
	}
	exec.RowsAffected()
	rows, err := tx.QueryContext(
		ctx,
		selectMatchingEvents,
		streamName,
		paramsJSON,
		fromPositionMarshaled,
		req.Direction.String(),
		req.Count,
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to execute query: %v", err)
	}

	defer rows.Close()

	var events []*eventstore.Event

	for rows.Next() {
		var event eventstore.Event
		var tagsBytes []byte
		var transactionID, globalID uint64
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
			"stream_name":    &event.StreamId,
			"stream_version": &event.Version,
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
			event.Tags = append(event.Tags, &eventstore.Tag{Key: key, Value: value})
		}

		// Set the Position
		event.Position = &eventstore.Position{
			CommitPosition:  transactionID,
			PreparePosition: globalID,
		}

		// Set the DateCreated
		event.DateCreated = timestamppb.New(dateCreated)

		events = append(events, &event)
	}

	return &eventstore.GetEventsResponse{Events: events}, nil
}

func getStreamSectionAsMap(streamName string, expectedVersion uint32, consistencyCondition *eventstore.Query) map[string]interface{} {
	lastRetrievedPositions := make(map[string]interface{})
	lastRetrievedPositions["stream_name"] = streamName
	lastRetrievedPositions["expected_version"] = expectedVersion

	if conditions := consistencyCondition; conditions != nil {
		lastRetrievedPositions["criteria"] = getCriteriaAsList(consistencyCondition)
	}

	return lastRetrievedPositions
}

func getConsistencyConditionAsMap(consistencyCondition *eventstore.IndexLockCondition) map[string]interface{} {
	lastRetrievedPositions := make(map[string]uint64)
	if consistencyCondition.ConsistencyMarker != nil {
		lastRetrievedPositions["transaction_id"] = consistencyCondition.ConsistencyMarker.CommitPosition
		lastRetrievedPositions["global_id"] = consistencyCondition.ConsistencyMarker.PreparePosition
	}

	criteriaList := getCriteriaAsList(consistencyCondition.Query)

	return map[string]interface{}{
		"last_retrieved_position": lastRetrievedPositions,
		"criteria":                criteriaList,
	}
}

func getCriteriaAsList(query *eventstore.Query) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(query.Criteria))
	for _, criterion := range query.Criteria {
		anded := make(map[string]interface{}, len(criterion.Tags))
		for _, tag := range criterion.Tags {
			anded[tag.Key] = tag.Value
		}
		result = append(result, anded)
	}
	return result
}

func PollEventsFromPgToNats(
	ctx context.Context,
	db *sql.DB,
	js jetstream.JetStream,
	eventStore *PostgresGetEvents,
	batchSize int32,
	lastPosition *eventstore.Position,
	logger logging.Logger,
	boundary string,
	schema string) error {

	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get database connection: %v", err)
	}
	defer conn.Close()

	// Begin a transaction
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %v", err)
	}
	defer tx.Rollback()

	// Try to acquire the lock with retries
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		hash := sha256.Sum256([]byte(boundary))
		lockID := int64(binary.BigEndian.Uint64(hash[:]))

		_, err = tx.ExecContext(ctx, fmt.Sprintf(setSearchPath, schema))
		if err != nil {
			return fmt.Errorf("failed to set search path: %v", err)
		}

		err = tx.QueryRowContext(ctx, "SELECT pg_advisory_xact_lock($1)", lockID).Err()
		if err != nil {
			logger.Errorf("Failed to acquire lock: %v, will retry", err)
			time.Sleep(5 * time.Second)
			continue
		}

		logger.Infof("Successfully acquired polling lock for %v", boundary)
		break
	}

	// Start polling loop
	for {
		if ctx.Err() != nil {
			logger.Error("Context cancelled, stopping polling")
			return ctx.Err()
		}

		logger.Debugf("Polling for boundary: %v", boundary)
		req := &eventstore.GetEventsRequest{
			FromPosition: lastPosition,
			Count:        batchSize,
			Direction:    eventstore.Direction_ASC,
			Boundary:     boundary,
		}
		resp, err := eventStore.Get(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to get events: %v", err)
		}

		logger.Debugf("Got %d events", len(resp.Events))

		for _, event := range resp.Events {
			eventData, err := json.Marshal(event)
			if err != nil {
				logger.Errorf("Failed to marshal event: %v", err)
				continue
			}
			publishEventWithRetry(
				ctx, js, eventData, eventstore.GetEventsSubjectName(boundary),
				logger, event.Position.PreparePosition, event.Position.CommitPosition,
			)
		}

		if len(resp.Events) > 0 {
			lastPosition = resp.Events[len(resp.Events)-1].Position
		}
		logger.Debugf(":%v Sleeping.....", boundary)
		time.Sleep(1 * time.Second) // Polling interval
	}
}

func publishEventWithRetry(ctx context.Context, js jetstream.JetStream, eventData []byte,
	subjectName string, logger logging.Logger, preparePosition uint64, commitPosition uint64) {
	backoff := time.Second
	maxBackoff := time.Minute * 5
	attempt := 1

	for {
		pubOpts := jetstream.PublishOpt(
			jetstream.WithMsgID(eventstore.GetEventNatsMessageId(int64(preparePosition), int64(commitPosition))),
		)
		_, err := js.Publish(ctx, subjectName, eventData, pubOpts)
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
