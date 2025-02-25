//go:build sqlite

package sqlite_eventstore

import (
	"context"
	"database/sql"
	"orisun/src/orisun/eventstore"
	"orisun/src/orisun/logging"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type SQLiteSaveEvents struct {
	db     *sql.DB
	logger logging.Logger
}

func NewSQLiteSaveEvents(db *sql.DB, logger *logging.Logger) *SQLiteSaveEvents {
	return &SQLiteSaveEvents{db: db, logger: *logger}
}

type SQLiteGetEvents struct {
	db     *sql.DB
	logger logging.Logger
}

func NewSQLiteGetEvents(db *sql.DB, logger *logging.Logger) *SQLiteGetEvents {
	return &SQLiteGetEvents{db: db, logger: *logger}
}

func (s *SQLiteSaveEvents) Save(
	ctx context.Context,
	events *[]eventstore.EventWithMapTags,
	consistencyCondition *eventstore.IndexLockCondition,
	boundary string,
	streamName string,
	expectedVersion uint32,
	streamConsistencyCondition *eventstore.Query) (transactionID string, globalID uint64, err error) {
	// Implementation for SQLite
	return "", 0, status.Errorf(codes.Unimplemented, "SQLite implementation not yet complete")
}

func (s *SQLiteGetEvents) Get(ctx context.Context, req *eventstore.GetEventsRequest) (*eventstore.GetEventsResponse, error) {
	// Implementation for SQLite
	return nil, status.Errorf(codes.Unimplemented, "SQLite implementation not yet complete")
}

type SQLiteLockProvider struct {
	db     *sql.DB
	logger logging.Logger
}

func NewSQLiteLockProvider(db *sql.DB, logger logging.Logger) *SQLiteLockProvider {
	return &SQLiteLockProvider{db: db, logger: logger}
}

func (m *SQLiteLockProvider) Lock(ctx context.Context, lockName string) (eventstore.UnlockFunc, error) {
	// Implementation for SQLite locking
	return nil, status.Errorf(codes.Unimplemented, "SQLite implementation not yet complete")
}
