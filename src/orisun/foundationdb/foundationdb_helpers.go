// package foundationdb_eventstore

// import (
//     "encoding/binary"
//     "encoding/json"
//     "fmt"
//     "sort"
//     "strings"
    
//     "github.com/apple/foundationdb/bindings/go/src/fdb"
//     "github.com/apple/foundationdb/bindings/go/src/fdb/tuple"
// 	eventstore "orisun/src/orisun/eventstore"
// )

// // getNextGlobalID atomically increments and returns the next global ID
// func getNextGlobalID(tr fdb.Transaction) (int64, error) {
//     counterKey := tuple.Tuple{globalIDPrefix, "counter"}.Pack()
    
//     // Atomic addition returns the new value
//     future := tr.Add(fdb.Key(counterKey), []byte{1})
//     result := future.MustGet()
    
//     return binary.BigEndian.Uint64(result), nil
// }

// // getLatestEventByTags finds the most recent event matching the given tags
// func getLatestEventByTags(tr fdb.Transaction, tags []*eventstore.Tag) (*eventstore.Event, error) {
//     // Sort tags for consistent ordering
//     normalizedTags := normalizeTags(tags)
    
//     // Create range for tag lookup
//     var events []*eventstore.Event
//     for _, tag := range normalizedTags {
//         tagKey := tuple.Tuple{tagPrefix, tag.Key, tag.Value}.Pack()
        
//         // Get range in reverse order to find latest
//         rr := tr.GetRange(fdb.KeyRange{
//             Begin: tagKey,
//             End:   fdb.Key(tagKey).Next(),
//         }, fdb.RangeOptions{
//             Limit:   1,
//             Reverse: true,
//         })
        
//         // Get the event referenced by this tag
//         iter := rr.Iterator()
//         if iter.Advance() {
//             kv := iter.MustGet()
//             // Extract transaction_id and global_id from tag key
//             _, txID, globalID := decodeFDBKey(kv.Key)
            
//             // Get actual event
//             eventKey := tuple.Tuple{eventPrefix, txID, globalID}.Pack()
//             eventData := tr.Get(fdb.Key(eventKey)).MustGet()
            
//             if eventData != nil {
//                 var event eventstore.Event
//                 if err := json.Unmarshal(eventData, &event); err != nil {
//                     return nil, err
//                 }
//                 events = append(events, &event)
//             }
//         }
//     }
    
//     // Return the latest event if any found
//     if len(events) > 0 {
//         return findLatestEvent(events), nil
//     }
//     return nil, nil
// }

// // getEventsByTags implements tag-based querying similar to PostgreSQL's @> ANY
// func getEventsByTags(tr fdb.Transaction, criteria *eventstore.Criteria, startKey tuple.Tuple,
// 	 count int32, direction eventstore.Direction) fdb.RangeResult {
//     var matchingEvents = make(map[string]*eventstore.Event)
    
//     for _, criterion := range criteria.Criteria {
//         normalizedTags := normalizeTags(criterion.Tags)
        
//         for _, tag := range normalizedTags {
//             tagKey := tuple.Tuple{tagPrefix, tag.Key, tag.Value}.Pack()
            
//             rr := tr.GetRange(fdb.KeyRange{
//                 Begin: tagKey,
//                 End:   fdb.Key(tagKey).Next(),
//             }, fdb.RangeOptions{
//                 Limit:   int(count),
//                 Reverse: direction == eventstore.Direction_DESC,
//             })
            
//             iter := rr.Iterator()
//             for iter.Advance() {
//                 kv := iter.MustGet()
//                 _, txID, globalID := decodeFDBKey(kv.Key)
                
//                 // Get the actual event
//                 eventKey := tuple.Tuple{eventPrefix, txID, globalID}.Pack()
//                 eventData := tr.Get(fdb.Key(eventKey)).MustGet()
                
//                 if eventData != nil {
//                     var event eventstore.Event
//                     if err := json.Unmarshal(eventData, &event); err != nil {
//                         continue
//                     }
                    
//                     // Use composite key for deduplication
//                     compositeKey := fmt.Sprintf("%d-%d", txID, globalID)
//                     matchingEvents[compositeKey] = &event
//                 }
//             }
//         }
//     }
    
//     // Convert matching events to sorted slice
//     return convertToRangeResult(matchingEvents, direction)
// }

// // Helper functions for consistent tag handling
// func normalizeTags(tags []*eventstore.Tag) []*eventstore.Tag {
//     normalized := make([]*eventstore.Tag, len(tags))
//     copy(normalized, tags)
    
//     sort.Slice(normalized, func(i, j int) bool {
//         if normalized[i].Key == normalized[j].Key {
//             return normalized[i].Value < normalized[j].Value
//         }
//         return normalized[i].Key < normalized[j].Key
//     })
    
//     return normalized
// }

// // isNewerPosition compares positions considering both transaction ID and global ID
// func isNewerPosition(a, b *eventstore.Position) bool {
//     if a.CommitPosition != b.CommitPosition {
//         return a.CommitPosition > b.CommitPosition
//     }
//     return a.PreparePosition > b.PreparePosition
// }

// // createTagKey creates a normalized, deterministic key for tags
// func createTagKey(tags []*eventstore.Tag) string {
//     normalized := normalizeTags(tags)
//     var parts []string
    
//     for _, tag := range normalized {
//         parts = append(parts, fmt.Sprintf("%s:%s", 
//             strings.ToLower(tag.Key), 
//             strings.ToLower(tag.Value)))
//     }
    
//     return strings.Join(parts, "|")
// }

// // Utility functions for key handling
// func decodeFDBKey(key []byte) (string, int64, int64) {
//     t, err := tuple.Unpack(key)
//     if err != nil {
//         return "", 0, 0
//     }
//     return t[0].(string), t[1].(int64), t[2].(int64)
// }

// func findLatestEvent(events []*eventstore.Event) *eventstore.Event {
//     if len(events) == 0 {
//         return nil
//     }
    
//     latest := events[0]
//     for _, event := range events[1:] {
//         if isNewerPosition(event.Position, latest.Position) {
//             latest = event
//         }
//     }
//     return latest
// } 