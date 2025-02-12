package postgres_eventstore

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	dbase "orisun/src/orisun/db"
	"orisun/src/orisun/eventstore"
	"orisun/src/orisun/logging"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type PostgresContainer struct {
	container testcontainers.Container
	host      string
	port      string
}

func setupTestContainer(t *testing.T) (*PostgresContainer, error) {
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "postgres:13",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "test",
			"POSTGRES_PASSWORD": "test",
			"POSTGRES_DB":       "testdb",
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections"),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start container: %v", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container host: %v", err)
	}

	port, err := container.MappedPort(ctx, "5432")
	if err != nil {
		return nil, fmt.Errorf("failed to get container port: %v", err)
	}

	return &PostgresContainer{
		container: container,
		host:      host,
		port:      port.Port(),
	}, nil
}

func setupTestDatabase(t *testing.T, container *PostgresContainer) (*sql.DB, error) {
	connStr := fmt.Sprintf(
		"host=%s port=%s user=test password=test dbname=testdb sslmode=disable",
		container.host,
		container.port,
	)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %v", err)
	}

	// Run database migrations using the common scripts
	if err := dbase.RunDbScripts(db, "test_boundary", false, context.Background()); err != nil {
		return nil, fmt.Errorf("failed to run database migrations: %v", err)
	}
	return db, nil
}

func TestSaveAndGetEvents(t *testing.T) {
	container, err := setupTestContainer(t)
	require.NoError(t, err)
	defer container.container.Terminate(context.Background())

	db, err := setupTestDatabase(t, container)
	require.NoError(t, err)
	defer db.Close()

	logger, err := logging.ZapLogger("debug")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	saveEvents := NewPostgresSaveEvents(db, &logger)
	getEvents := NewPostgresGetEvents(db, &logger)

	// Test saving events
	events := []eventstore.EventWithMapTags{
		{
			EventId:   "test-event-1",
			EventType: "TestEvent",
			Data:      "{\"key\": \"value\"}",
			Metadata:  "{\"meta\": \"data\"}",
			Tags: map[string]interface{}{
				"tag1": "value1",
				"tag2": "value2",
			},
		},
	}

	// Save events
	tranID, globalID, err := saveEvents.Save(
		context.Background(),
		&events,
		nil,
		"test_boundary",
		"test-stream",
		0,
		nil,
	)

	assert.NoError(t, err)
	assert.NotEmpty(t, tranID)
	assert.Greater(t, globalID, uint64(0))

	// Get events
	resp, err := getEvents.Get(context.Background(), &eventstore.GetEventsRequest{
		Boundary:  "test_boundary",
		Direction: eventstore.Direction_ASC,
		Count:     10,
		Stream: &eventstore.SaveStreamQuery{
			Name: "test-stream",
		},
	})

	assert.NoError(t, err)
	assert.Len(t, resp.Events, 1)
	assert.Equal(t, "test-event-1", resp.Events[0].EventId)
	assert.Equal(t, "TestEvent", resp.Events[0].EventType)
	assert.Equal(t, "{\"key\": \"value\"}", resp.Events[0].Data)
	assert.Equal(t, "{\"meta\": \"data\"}", resp.Events[0].Metadata)

	// Verify tags
	assert.Len(t, resp.Events[0].Tags, 2)
	for _, tag := range resp.Events[0].Tags {
		switch tag.Key {
		case "tag1":
			assert.Equal(t, "value1", tag.Value)
		case "tag2":
			assert.Equal(t, "value2", tag.Value)
		default:
			t.Errorf("unexpected tag key: %s", tag.Key)
		}
	}
}
