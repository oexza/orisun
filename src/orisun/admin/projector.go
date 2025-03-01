package admin

import (
	"context"
	"encoding/json"
	"orisun/src/orisun/eventstore"
	l "orisun/src/orisun/logging"
	"time"
)

type UserProjector struct {
	db         DB
	logger     l.Logger
	boundary   string
	eventStore *eventstore.EventStore
	username   string
	password   string
}

func NewUserProjector(db DB, logger l.Logger, eventStore *eventstore.EventStore,
	boundary string, username, password string) *UserProjector {

	return &UserProjector{
		db:         db,
		logger:     logger,
		boundary:   boundary,
		eventStore: eventStore,
		username:   username,
		password:   password,
	}
}

func (p *UserProjector) Start(ctx context.Context) error {
	p.logger.Info("Starting user projector")
	var projectorName = "user-projector"
	// Get last checkpoint
	pos, err := p.db.GetProjectorLastPosition(projectorName)
	if err != nil {
		return err
	}

	stream := eventstore.NewCustomEventStream(ctx)

	go func() {
		for {
			p.logger.Debugf("Receiving events for: %s", projectorName)
			event, err := stream.Recv()
			if err != nil {
				p.logger.Error("Error receiving event: %v", err)
				continue
			}

			for {
				if err := p.handleEvent(event); err != nil {
					p.logger.Error("Error handling event: %v", err)

					time.Sleep(5 * time.Second)
					continue
				}

				var pos = eventstore.Position{
					CommitPosition:  event.Position.CommitPosition,
					PreparePosition: event.Position.PreparePosition,
				}

				// Update checkpoint
				err := p.db.UpdateProjectorPosition(
					projectorName,
					&pos,
				)

				if err != nil {
					p.logger.Error("Error updating checkpoint: %v", err)
					time.Sleep(5 * time.Second)
					continue
				}
				break
			}
		}
	}()
	// Subscribe from last checkpoint
	err = p.eventStore.SubscribeToEvents(
		ctx,
		p.boundary,
		projectorName,
		pos,
		nil,
		stream,
	)
	if err != nil {
		return err
	}
	return nil
}

func (p *UserProjector) handleEvent(event *eventstore.Event) error {
	p.logger.Debug("Handling event %v", event)

	switch event.EventType {
	case EventTypeUserCreated:
		var userEvent UserCreated
		if err := json.Unmarshal([]byte(event.Data), &userEvent); err != nil {
			return err
		}

		err := p.db.CreateNewUser(
			userEvent.UserId,
			userEvent.Username,
			userEvent.PasswordHash,
			userEvent.Roles,
		)
		if err != nil {
			return err
		}

	case EventTypeUserDeleted:
		var userEvent UserDeleted
		if err := json.Unmarshal([]byte(event.Data), &userEvent); err != nil {
			return err
		}

		err := p.db.DeleteUser(userEvent.UserId)
		if err != nil {
			return err
		}

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
	return nil
}
