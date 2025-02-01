package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	pb "orisun/src/orisun/eventstore"
	l "orisun/src/orisun/logging"
	"strings"
)

type UserProjector struct {
	db         *sql.DB
	logger     l.Logger
	schema     string
	eventStore pb.EventStoreClient
	username   string
	password   string
}

func NewUserProjector(db *sql.DB, logger l.Logger, eventStore pb.EventStoreClient, schema string, username, password string) *UserProjector {
	if schema == "" {
		schema = "public"
	}
	return &UserProjector{
		db:         db,
		logger:     logger,
		schema:     schema,
		eventStore: eventStore,
		username:   username,
		password:   password,
	}
}

func (p *UserProjector) Start(ctx context.Context) error {
	p.logger.Info("Starting user projector")
	// Get last checkpoint
	var commitPos, preparePos uint64
	err := p.db.QueryRow(
		fmt.Sprintf("SELECT COALESCE(commit_position, 0), COALESCE(prepare_position, 0) FROM %s.user_projector_checkpoint", p.schema),
	).Scan(&commitPos, &preparePos)
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	// Subscribe from last checkpoint
	stream, err := p.eventStore.CatchUpSubscribeToEvents(ctx, &pb.CatchUpSubscribeToEventStoreRequest{
		SubscriberName: "user_projector",
		Boundary:       p.schema,
		Position: &pb.Position{
			CommitPosition:  commitPos,
			PreparePosition: preparePos,
		},
		Query: &pb.Query{
			Criteria: []*pb.Criterion{
				{
					Tags: []*pb.Tag{},
				},
			},
		},
	},
	)
	if err != nil {
		return err
	}

	go func() {
		for {
			event, err := stream.Recv()
			if err != nil {
				p.logger.Error("Error receiving event: %v", err)
				continue
			}

			if err := p.handleEvent(event); err != nil {
				p.logger.Error("Error handling event: %v", err)
				continue
			}

			// Update checkpoint
			if _, err := p.db.Exec(
				fmt.Sprintf("INSERT INTO %s.user_projector_checkpoint (commit_position, prepare_position) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET commit_position = $1, prepare_position = $2", p.schema),
				event.Position.CommitPosition,
				event.Position.PreparePosition,
			); err != nil {
				p.logger.Error("Error updating checkpoint: %v", err)
			}
		}
	}()

	return nil
}

func (p *UserProjector) handleEvent(event *pb.Event) error {
	p.logger.Debug("Handling event %v", event)
	tx, err := p.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	switch event.EventType {
	case EventTypeUserCreated:
		var userEvent UserCreated
		if err := json.Unmarshal([]byte(event.Data), &userEvent); err != nil {
			return err
		}

		rolesStr := "{" + strings.Join(userEvent.Roles, ",") + "}"
		_, err = tx.Exec(
			fmt.Sprintf("INSERT INTO %s.users (username, password_hash, roles) VALUES ($1, $2, $3)",
				p.schema),
			userEvent.Username, userEvent.PasswordHash, rolesStr,
		)

	case EventTypeUserDeleted:
		var userEvent UserDeleted
		if err := json.Unmarshal([]byte(event.Data), &userEvent); err != nil {
			return err
		}
		_, err = tx.Exec(
			fmt.Sprintf("DELETE FROM %s.users WHERE username = $1",
				p.schema),
			userEvent.Username,
		)

		// case EventTypeRolesChanged:
		// 	_, err = tx.Exec(
		// 		fmt.Sprintf("UPDATE %s.users SET roles = $1 WHERE username = $2",
		// 			p.schema),
		// 		userEvent.Roles, userEvent.Username,
		// 	)

		// case EventTypePasswordChanged:
		// 	_, err = tx.Exec(
		// 		fmt.Sprintf("UPDATE %s.users SET password_hash = $1 WHERE username = $2",
		// 			p.schema),
		// 		userEvent.PasswordHash, userEvent.Username,
		// 	)
	}

	if err != nil {
		return err
	}

	return tx.Commit()
}
