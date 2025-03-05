package admin

import (
	// "bytes"
	"encoding/json"
	"errors"
	"time"

	// "fmt"
	"html/template"
	"net/http"
	pb "orisun/src/orisun/eventstore"
	l "orisun/src/orisun/logging"
	"strings"

	"github.com/go-chi/chi/v5"
	datastar "github.com/starfederation/datastar/sdk/go"
)

type contextKey string

const (
	contextKeyUser   = contextKey("user")
	userStreamPrefix = "User-Registration:::::"
	registrationTag  = "Registration"
	usernameTag      = "Registration_username"
)

type DB interface {
	ListAdminUsers() ([]*User, error)
	GetProjectorLastPosition(projectorName string) (*pb.Position, error)
	UpdateProjectorPosition(name string, position *pb.Position) error
	CreateNewUser(id string, username string, password_hash string, roles []Role) error
	DeleteUser(id string) error
	GetUserByUsername(username string) (User, error)
}

type AdminServer struct {
	logger               l.Logger
	tmpl                 *template.Template
	router               *chi.Mux
	eventStore           *pb.EventStore
	adminCommandHandlers AdminCommandHandlers
}

func NewAdminServer(logger l.Logger, eventStore *pb.EventStore, adminCommandHandlers AdminCommandHandlers) (*AdminServer, error) {
	funcMap := template.FuncMap{
		"join": strings.Join,
	}

	tmpl := template.Must(template.New("").Funcs(funcMap).ParseFS(content, "templates/*.html"))

	router := chi.NewRouter()

	server := &AdminServer{
		logger:               logger,
		tmpl:                 tmpl,
		router:               router,
		eventStore:           eventStore,
		adminCommandHandlers: adminCommandHandlers,
	}

	var userExistsError UserExistsError
	if err := adminCommandHandlers.createUser("admin", "changeit", []Role{RoleAdmin}); err != nil && !errors.As(err, &userExistsError) {
		return nil, err
	}

	// Register routes
	router.Route("/admin", func(r chi.Router) {
		// Add login routes
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			tmpl.ExecuteTemplate(w, "hello-world.html", nil)
		})
		r.Get("/login", server.handleLoginPage)
		r.Post("/login", server.handleLogin)

		// Existing routes
		r.Get("/users", server.handleUsers)
		r.Post("/users", server.handleCreateUser)
		r.Get("/users/list", server.handleUsersList)
		r.Delete("/users/{username}", server.handleUserDelete)

		r.Get("/hello-world", func(w http.ResponseWriter, r *http.Request) {
			const message = "A Orisun Datastar!!!"

			type Store struct {
				Delay time.Duration `json:"delay"` // delay in milliseconds between each character of the message.
			}
			store := &Store{}
			if err := datastar.ReadSignals(r, store); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			sse := datastar.NewSSE(w, r)

			for i := 0; i < len(message); i++ {
				sse.MergeFragments(`<div id="message">` + message[:i+1] + `</div>`)
				time.Sleep(store.Delay * time.Millisecond)
			}
		})
	})

	return server, nil
}

// Add these new handlers

func (s *AdminServer) handleLoginPage(w http.ResponseWriter, r *http.Request) {
    err := s.tmpl.ExecuteTemplate(w, "login.html", nil)
    if err != nil {
        s.logger.Errorf("Template execution error: %v", err)
        http.Error(w, "Internal Server Error", http.StatusInternalServerError)
        return
    }
}

type LoginRequest struct {
	Username string
	Password string
}

func (s *AdminServer) handleLogin(w http.ResponseWriter, r *http.Request) {

	store := &LoginRequest{}
	if err := datastar.ReadSignals(r, store); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sse := datastar.NewSSE(w, r)

	// Validate credentials
	user, err := s.adminCommandHandlers.login(store.Username, store.Password)
	if err != nil {
		sse.RemoveFragments("message")
		sse.MergeFragments(`<div id="message">` + `Login Failed` + `</div>`)
		return
	}

	userAsString, err := json.Marshal(user)
	if err != nil {
		sse.MergeFragments(`<div id="message">` + `Login Failed` + `</div>`)
	}
	// Set the token as an HTTP-only cookie
	http.SetCookie(w, &http.Cookie{
		Name:  "auth",
		Value: string(userAsString),
		// Expires:  ,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		Path:     "/",
	})

	sse.RemoveFragments("message")
	sse.MergeFragments(`<div id="message">` + `Login Succeded` + `</div>`)

	// Redirect to users page after successful login
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (s *AdminServer) handleUsers(w http.ResponseWriter, r *http.Request) {
	s.tmpl.ExecuteTemplate(w, "users.html", nil)
}

func (s *AdminServer) handleUsersList(w http.ResponseWriter, r *http.Request) {
	users, err := s.adminCommandHandlers.listUsers()
	if err != nil {
		http.Error(w, "Failed to list users", http.StatusInternalServerError)
		return
	}

	data := struct {
		Users       []*User
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

	// Convert []string to []Role
	rolesList := make([]Role, len(roles))
	for i, r := range roles {
		rolesList[i] = Role(r)
	}

	if err := s.adminCommandHandlers.createUser(username, password, rolesList); err != nil {
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

	if err := s.adminCommandHandlers.deleteUser(userId); err != nil {
		http.Error(w, "Failed to delete user "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *AdminServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}
