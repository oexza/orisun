package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	pb "orisun/src/orisun/eventstore"
	l "orisun/src/orisun/logging"
	"strings"
)

type AdminCommandHandlers struct {
	eventStore    *pb.EventStore
	db            DB
	logger        l.Logger
	boundary      string
	authenticator *Authenticator
}

func NewAdminCommandHandlers(eventStore *pb.EventStore, db DB, logger l.Logger, boundary string, authenticator *Authenticator) *AdminCommandHandlers {
	return &AdminCommandHandlers{
		eventStore:    eventStore,
		db:            db,
		logger:        logger,
		boundary:      boundary,
		authenticator: authenticator,
	}
}

func (s *AdminCommandHandlers) listUsers() ([]*User, error) {
	return s.db.ListAdminUsers()
}

func (s *AdminCommandHandlers) createUser(username, password string, roles []Role) error {
	username = strings.TrimSpace(username)
	events := []*Event{}

	userCreatedEvent, err := s.eventStore.GetEvents(
		context.Background(),
		&pb.GetEventsRequest{
			Boundary:  s.boundary,
			Direction: pb.Direction_DESC,
			Count:     1,
			Query: &pb.Query{
				Criteria: []*pb.Criterion{
					{
						Tags: []*pb.Tag{
							{Key: usernameTag, Value: username},
							{Key: "eventType", Value: EventTypeUserCreated},
						},
					},
				},
			},
		},
	)

	if err != nil {
		return err
	}

	for _, event := range userCreatedEvent.Events {
		var userCreatedEvent = UserCreated{}
		err := json.Unmarshal([]byte(event.Data), &userCreatedEvent)
		if err != nil {
			return err
		}
		events = append(events, &Event{
			EventType: event.EventType,
			Data:      userCreatedEvent,
		})
	}

	if len(userCreatedEvent.Events) > 0 {
		userCreatedEvent := events[0].Data.(UserCreated)
		UserDeletedEvent, err := s.eventStore.GetEvents(
			context.Background(),
			&pb.GetEventsRequest{
				Boundary:  s.boundary,
				Direction: pb.Direction_DESC,
				Count:     1,
				Stream:    &pb.GetStreamQuery{Name: userStreamPrefix + userCreatedEvent.UserId},
				Query: &pb.Query{
					Criteria: []*pb.Criterion{
						{
							Tags: []*pb.Tag{
								{Key: "eventType", Value: EventTypeUserDeleted},
							},
						},
					},
				},
			},
		)

		if err != nil {
			return err
		}

		for _, event := range UserDeletedEvent.Events {
			var userDeletedEvent = UserDeleted{}
			err := json.Unmarshal([]byte(event.Data), &userDeletedEvent)
			if err != nil {
				return err
			}
			events = append(events, &Event{
				EventType: event.EventType,
				Data:      userDeletedEvent,
			})
		}
	}

	userId, err := uuid.NewV7()
	if err != nil {
		return err
	}

	newEvents, err := CreateUserCommandHandler(userId.String(), username, password, roles, events)

	if err != nil {
		return err
	}

	eventsToSave := []*pb.EventToSave{}

	for _, event := range newEvents {
		eventData, err := json.Marshal(event.Data)
		if err != nil {
			return err
		}
		eventId, err := uuid.NewV7()
		if err != nil {
			return err
		}
		eventsToSave = append(eventsToSave, &pb.EventToSave{
			EventId:   eventId.String(),
			EventType: event.EventType,
			Data:      string(eventData),
			Tags: []*pb.Tag{
				{Key: usernameTag, Value: username},
				{Key: registrationTag, Value: username},
			},
			Metadata: "{\"schema\":\"" + s.boundary + "\",\"createdBy\":\"" + username + "\"}",
		})
	}

	_, err = s.eventStore.SaveEvents(context.Background(), &pb.SaveEventsRequest{
		Boundary: s.boundary,
		ConsistencyCondition: &pb.IndexLockCondition{
			ConsistencyMarker: &pb.Position{
				PreparePosition: 0,
				CommitPosition:  0,
			},
			Query: &pb.Query{
				Criteria: []*pb.Criterion{
					{
						Tags: []*pb.Tag{
							{Key: usernameTag, Value: username},
						},
					},
				},
			},
		},
		Events: eventsToSave,
		Stream: &pb.SaveStreamQuery{
			Name:            userStreamPrefix + userId.String(),
			ExpectedVersion: 0,
		},
	})

	return err
}

func CreateUserCommandHandler(userId string, username, password string, roles []Role, events []*Event) ([]*Event, error) {
	//check if events contains any user created event and not user deleted event
	if len(events) > 0 {
		hasUserCreated := false
		hasUserDeleted := false
		for _, event := range events {
			switch event.EventType {
			case EventTypeUserCreated:
				hasUserCreated = true
			case EventTypeUserDeleted:
				hasUserDeleted = true
			}
		}
		if hasUserCreated && !hasUserDeleted {
			return nil, UserExistsError{
				username: username,
			}
		}
	}

	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}

	// Create user created event
	event := Event{
		EventType: EventTypeUserCreated,
		Data: UserCreated{
			Username:     username,
			Roles:        roles,
			PasswordHash: string(hash),
			UserId:       userId,
		},
	}
	return []*Event{&event}, nil
}

type UserExistsError struct {
	username string
}

func (e UserExistsError) Error() string {
	return fmt.Sprintf("username %s already exists", e.username)
}

func (s *AdminCommandHandlers) deleteUser(userId string) error {
	userId = strings.TrimSpace(userId)
	events, err := s.eventStore.GetEvents(
		context.Background(),
		&pb.GetEventsRequest{
			Boundary:  s.boundary,
			Direction: pb.Direction_DESC,
			Count:     1,
			Stream: &pb.GetStreamQuery{
				Name: userStreamPrefix + userId,
			},
		},
	)
	if err != nil {
		return err
	}

	if len(events.Events) > 0 {
		if events.Events[0].EventType == EventTypeUserDeleted {
			return fmt.Errorf("error: user already deleted")
		}

		event := UserDeleted{
			UserId: userId,
		}

		eventData, err := json.Marshal(event)
		if err != nil {
			return err
		}

		// Store event
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		_, err = s.eventStore.SaveEvents(context.Background(), &pb.SaveEventsRequest{
			Boundary:             s.boundary,
			ConsistencyCondition: nil,
			Stream: &pb.SaveStreamQuery{
				Name: userStreamPrefix + userId,
			},
			Events: []*pb.EventToSave{{
				EventId:   id.String(),
				EventType: EventTypeUserDeleted,
				Data:      string(eventData),
				Tags: []*pb.Tag{
					{Key: registrationTag, Value: userId},
				},
				Metadata: "{\"schema\":\"" + s.boundary + "\",\"createdBy\":\"" + id.String() + "\"}",
			}},
		})
	}
	return fmt.Errorf("error: user not found")
}

func (s *AdminCommandHandlers) login(username, password string) (User, error) {
	user, err := s.authenticator.ValidateCredentials(username, password)

	if err != nil {
		return User{}, err
	}

	return user, nil
}
