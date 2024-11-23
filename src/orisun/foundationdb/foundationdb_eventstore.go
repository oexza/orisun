package foundationdb_eventstore

import (
    "context"
    "encoding/json"
    "fmt"
    "time"
    
    "github.com/apple/foundationdb/bindings/go/src/fdb"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
    "google.golang.org/protobuf/types/known/timestamppb"
	eventstore "orisun/src/orisun/eventstore"
)

type FoundationDBSaveEvents struct {
    Db fdb.Database
}

type FoundationDBGetEvents struct {
    Db fdb.Database
}

// Directory structure for FDB
var dir = fdb.DirectorySubspace{
    events:     []string{"events"},
    positions:  []string{"positions"},
    tags:       []string{"tags"},
    locks:      []string{"locks"},
}

func (s *FoundationDBSaveEvents) Save(ctx context.Context, events *[]eventstore.EventWithMapTags, consistencyCondition *eventstore.ConsistencyCondition) (transactionID string, globalID int64, err error) {
    result, err := s.Db.Transact(func(tr fdb.Transaction) (interface{}, error) {
        // Check consistency if required
        if consistencyCondition != nil {
            ok, err := checkConsistency(tr, consistencyCondition)
            if err != nil {
                return nil, status.Errorf(codes.Internal, "consistency check failed: %v", err)
            }
            if !ok {
                return nil, status.Errorf(codes.FailedPrecondition, "consistency condition not met")
            }
        }

        // Get current position
        currentPos, err := getCurrentPosition(tr)
        if err != nil {
            return nil, status.Errorf(codes.Internal, "failed to get current position: %v", err)
        }

        // Store events
        for _, event := range *events {
            eventKey := dir.events.Pack(tuple.Tuple{currentPos.GlobalPosition})
            eventData, err := json.Marshal(event)
            if err != nil {
                return nil, status.Errorf(codes.Internal, "failed to marshal event: %v", err)
            }
            
            tr.Set(eventKey, eventData)

            // Store tag indices
            for key, value := range event.Tags {
                tagKey := dir.tags.Pack(tuple.Tuple{key, value, currentPos.GlobalPosition})
                tr.Set(tagKey, nil)
            }

            currentPos.GlobalPosition++
        }

        // Update position
        posKey := dir.positions.Pack(tuple.Tuple{"current"})
        posData, err := json.Marshal(currentPos)
        if err != nil {
            return nil, status.Errorf(codes.Internal, "failed to marshal position: %v", err)
        }
        tr.Set(posKey, posData)

        return currentPos, nil
    })

    if err != nil {
        return "", 0, err
    }

    pos := result.(*Position)
    return fmt.Sprintf("%d", pos.CommitPosition), pos.GlobalPosition, nil
}

func (s *FoundationDBGetEvents) Get(ctx context.Context, req *eventstore.GetEventsRequest) (*eventstore.GetEventsResponse, error) {
    result, err := s.Db.Transact(func(tr fdb.Transaction) (interface{}, error) {
        var events []*eventstore.Event
        
        if req.Criteria != nil && len(req.Criteria.Criteria) > 0 {
            return getEventsByTags(tr, req)
        }

        // Get events by range
        startPos := req.LastRetrievedPosition.PreparePosition
        endPos := startPos + int64(req.Count)
        
        if req.Direction == eventstore.Direction_DESC {
            startPos, endPos = endPos, startPos
        }

        eventRange := dir.events.Range(tuple.Tuple{startPos}, tuple.Tuple{endPos})
        iter := tr.GetRange(eventRange.Begin, eventRange.End, fdb.RangeOptions{Reverse: req.Direction == eventstore.Direction_DESC}).Iterator()

        for iter.Advance() {
            var event eventstore.Event
            if err := json.Unmarshal(iter.MustGet().Value, &event); err != nil {
                return nil, status.Errorf(codes.Internal, "failed to unmarshal event: %v", err)
            }
            event.DateCreated = timestamppb.Now() // FDB doesn't store timestamps, consider adding to event data
            events = append(events, &event)
        }

        return events, nil
    })

    if err != nil {
        return nil, err
    }

    return &eventstore.GetEventsResponse{Events: result.([]*eventstore.Event)}, nil
}

// Helper functions would follow, including:
// - checkConsistency
// - getCurrentPosition
// - getEventsByTags
// - polling implementation similar to PostgreSQL version

func PollEventsFromFDBToNats(ctx context.Context, db fdb.Database, js nats.JetStreamContext,
    eventStore *eventstore.EventStore, batchSize int32, lastPosition *eventstore.Position, eventsSubjectName string) {
    // Similar implementation to PostgreSQL version, but using FDB's lock mechanism
    // ...
} 