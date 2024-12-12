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

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type contextKey string

const (
	contextKeyUser = contextKey("user")
)

const (
	userTag = "user"
)

type AdminServer struct {
	db         *sql.DB
	logger     l.Logger
	tmpl       *template.Template
	mux        *http.ServeMux
	eventStore pb.EventStoreServer
	schema     string
}

type User struct {
	Username string
	Roles    []string
}

// Event types
const (
	EventTypeUserCreated     = "$UserCreated"
	EventTypeUserDeleted     = "$UserDeleted"
	EventTypeRolesChanged    = "$RolesChanged"
	EventTypePasswordChanged = "$PasswordChanged"
)

type UserEvent struct {
	Username     string   `json:"username"`
	Roles        []string `json:"roles,omitempty"`
	PasswordHash string   `json:"password_hash,omitempty"`
}

func NewAdminServer(db *sql.DB, logger l.Logger, eventStore pb.EventStoreServer, schema string) *AdminServer {
	funcMap := template.FuncMap{
		"join": strings.Join,
	}

	tmpl := template.Must(template.New("").Funcs(funcMap).ParseFS(content, "templates/*.html"))

	server := &AdminServer{
		db:         db,
		logger:     logger,
		tmpl:       tmpl,
		mux:        http.NewServeMux(),
		eventStore: eventStore,
		schema:     schema,
	}

	// Register routes
	server.mux.HandleFunc("/admin/users", server.handleUsers)
	server.mux.HandleFunc("/admin/users/list", server.handleUsersList)
	server.mux.HandleFunc("/admin/users/", server.handleUserDelete)

	return server
}

func (s *AdminServer) handleUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.tmpl.ExecuteTemplate(w, "users.html", nil)
	case http.MethodPost:
		s.handleCreateUser(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *AdminServer) handleUsersList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

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
		CurrentUser: r.Context().Value(contextKeyUser).(string),
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
	user := User{Username: username, Roles: roles}
	data := struct {
		User        User
		CurrentUser string
	}{
		User:        user,
		CurrentUser: "r.Context().Value(contextKeyUser).(string)",
	}
	s.tmpl.ExecuteTemplate(w, "user-row.html", data)
}

func (s *AdminServer) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 4 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	username := parts[3]
	currentUser := r.Context().Value(contextKeyUser).(string)

	if username == currentUser {
		http.Error(w, "Cannot delete your own account", http.StatusBadRequest)
		return
	}

	if err := s.deleteUser(username); err != nil {
		http.Error(w, "Failed to delete user", http.StatusInternalServerError)
		return
	}

	// Return empty response as the element will be removed
	w.WriteHeader(http.StatusOK)
}

func (s *AdminServer) listUsers() ([]User, error) {
	rows, err := s.db.Query("SELECT username, roles FROM users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.Username, &user.Roles); err != nil {
			s.logger.Error("Failed to scan user row: %v", err)
			continue
		}
		users = append(users, user)
	}

	return users, nil
}

func (s *AdminServer) createUser(username, password string, roles []string) error {
	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	events, err := s.eventStore.GetEvents(context.Background(), &pb.GetEventsRequest{
		Boundary:  s.schema,
		Direction: pb.Direction_DESC,
		Count:     1,
		LastRetrievedPosition: &pb.Position{
			PreparePosition: 999999999999999999,
			CommitPosition:  999999999999999999,
		},
		Criteria: &pb.Criteria{
			Criteria: []*pb.Criterion{
				{
					Tags: []*pb.Tag{
						{Key: userTag, Value: username},
						{Key: "eventType", Value: EventTypeUserCreated},
					},
				},
			},
		},
	})

	if err != nil {
		return err
	}

	s.logger.Debugf("events: %v", events)

	if len(events.Events) > 0 {
		return fmt.Errorf("user already exists")
	}

	// Create user created event
	event := UserEvent{
		Username:     username,
		Roles:        roles,
		PasswordHash: string(hash),
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
	consistencyCondition := pb.ConsistencyCondition{
		ConsistencyMarker: &pb.Position{
			PreparePosition: 00,
			CommitPosition:  00,
		},
		Criteria: &pb.Criteria{
			Criteria: []*pb.Criterion{
				{
					Tags: []*pb.Tag{
						{Key: userTag, Value: username},
						{Key: "eventType", Value: EventTypeUserCreated},
					},
				},
			},
		},
	}
	_, err = s.eventStore.SaveEvents(context.Background(), &pb.SaveEventsRequest{
		Boundary:             s.schema,
		ConsistencyCondition: &consistencyCondition,
		Events: []*pb.EventToSave{
			{
				EventId:   id.String(),
				EventType: EventTypeUserCreated,
				Data:      string(eventData),
				Tags: []*pb.Tag{
					{Key: userTag, Value: username},
					{Key: "eventType", Value: EventTypeUserCreated},
				},
				Metadata: "{\"schema\":\"" + s.schema + "\"}",
			},
		},
	})

	return err
}

func (s *AdminServer) deleteUser(username string) error {
	event := UserEvent{
		Username: username,
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
		Boundary: s.schema,
		Events: []*pb.EventToSave{{
			EventId:   id.String(),
			EventType: EventTypeUserDeleted,
			Data:      string(eventData),
			Tags:      []*pb.Tag{{Key: userTag, Value: username}},
		}},
	})

	return err
}

func (s *AdminServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

