package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	// "fmt"
	"html/template"
	"net/http"
	pb "orisun/src/orisun/eventstore"
	l "orisun/src/orisun/logging"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/lib/pq"
	// datastar "github.com/starfederation/datastar/sdk/go"
	"golang.org/x/crypto/bcrypt"
)

type contextKey string

const (
	contextKeyUser   = contextKey("user")
	userStreamPrefix = "User-Registration:::::"
	registrationTag  = "Registration"
	usernameTag      = "Registration_username"
)

type AdminServer struct {
	db         *sql.DB
	logger     l.Logger
	tmpl       *template.Template
	router     *chi.Mux
	eventStore *pb.EventStore
	boundary   string
}

type User struct {
	Username string
	Roles    []string
}

func NewAdminServer(db *sql.DB, logger l.Logger, eventStore *pb.EventStore, schema string, boundary string) (*AdminServer, error) {
	funcMap := template.FuncMap{
		"join": strings.Join,
	}

	tmpl := template.Must(template.New("").Funcs(funcMap).ParseFS(content, "templates/*.html"))

	router := chi.NewRouter()

	server := &AdminServer{
		db:         db,
		logger:     logger,
		tmpl:       tmpl,
		router:     router,
		eventStore: eventStore,
		boundary:   boundary,
	}

	events, err := eventStore.GetEvents(
		context.Background(),
		&pb.GetEventsRequest{
			Boundary: boundary,
			Count:    1,
			Direction: pb.Direction_DESC,
			Query: &pb.Query{
				Criteria: []*pb.Criterion{
					{
						Tags: []*pb.Tag{
							{Key: usernameTag, Value: "admin"},
							{Key: "eventType", Value: EventTypeUserCreated},
						},
					},
				},
			},
		},
	)

	if err != nil {
		return nil, err
	}

	logger.Debugf("events from handler: %v", events)

	if len(events.Events) == 0 {
		err := server.createUser(
			"admin", "changeit", []string{"admin"},
		)
		if err != nil {
			return nil, err
		}
	}

	// Register routes
	router.Route("/admin", func(r chi.Router) {
		r.Get("/users", server.handleUsers)
		r.Post("/users", server.handleCreateUser)
		r.Get("/users/list", server.handleUsersList)
		r.Delete("/users/{username}", server.handleUserDelete)
	})

	return server, nil
}

func (s *AdminServer) handleUsers(w http.ResponseWriter, r *http.Request) {
	s.tmpl.ExecuteTemplate(w, "users.html", nil)
}

func (s *AdminServer) handleUsersList(w http.ResponseWriter, r *http.Request) {
	users, err := s.listUsers()
	if err != nil {
		http.Error(w, "Failed to list users", http.StatusInternalServerError)
		return
	}

	data := struct {
		Users       []User
		CurrentUser string
	}{
		Users:       users,
		CurrentUser: "r.Context().Value(contextKeyUser).(string)",
	}

	s.tmpl.ExecuteTemplate(w, "user-list.html", data)
}

func (s *AdminServer) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")
	roles := r.Form["roles"]

	if err := s.createUser(username, password, roles); err != nil {
		http.Error(w, "Failed to create user "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Return just the new user row
	// user := User{Username: username, Roles: roles}
	// data := struct {
	// 	User        User
	// 	CurrentUser string
	// }{
	// 	User:        user,
	// 	CurrentUser: "r.Context().Value(contextKeyUser).(string)",
	// }
	w.WriteHeader(http.StatusNoContent)
	// s.tmpl.ExecuteTemplate(w, "user-row.html", data)
}

func (s *AdminServer) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	userId := chi.URLParam(r, "userId")
	currentUser := "r.Context().Value(contextKeyUser).(string)"

	if userId == currentUser {
		http.Error(w, "Cannot delete your own account", http.StatusBadRequest)
		return
	}

	if err := s.deleteUser(userId); err != nil {
		http.Error(w, "Failed to delete user "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *AdminServer) listUsers() ([]User, error) {
	rows, err := s.db.Query("SELECT id, username, roles FROM users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.Username, pq.Array(&user.Roles)); err != nil {
			s.logger.Error("Failed to scan user row: %v", err)
			continue
		}
		users = append(users, user)
	}

	return users, nil
}

func (s *AdminServer) createUser(username, password string, roles []string) error {
	username = strings.TrimSpace(username)
	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	events, err := s.eventStore.GetEvents(
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

	s.logger.Debugf("events: %v", events)

	if len(events.Events) > 0 {
		event := events.Events[0]
		var userCreatedEvent = UserCreated{}

		err := json.Unmarshal([]byte(event.Data), &UserCreated{})
		if err != nil {
			return err
		}
		events, err := s.eventStore.GetEvents(
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
		if len(events.Events) == 0 {
			return fmt.Errorf("error: user already exists")
		}
	}

	userId, err := uuid.NewV7()
	if err != nil {
		return err
	}
	// Create user created event
	event := UserCreated{
		Username:     username,
		Roles:        roles,
		PasswordHash: string(hash),
		UserId:       userId.String(),
	}

	eventData, err := json.Marshal(event)
	if err != nil {
		return err
	}

	if err != nil {
		return err
	}

	eventId, err := uuid.NewV7()
	if err != nil {
		return err
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
		Events: []*pb.EventToSave{
			{
				EventId:   eventId.String(),
				EventType: EventTypeUserCreated,
				Data:      string(eventData),
				Tags: []*pb.Tag{
					{Key: usernameTag, Value: username},
					{Key: registrationTag, Value: userId.String()},
				},
				Metadata: "{\"schema\":\"" + s.boundary + "\",\"createdBy\":\"" + (userId).String() + "\"}",
			},
		},
		Stream: &pb.SaveStreamQuery{
			Name: userStreamPrefix + userId.String(),
		},
	})

	return err
}

func (s *AdminServer) deleteUser(userId string) error {
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

func (s *AdminServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}
