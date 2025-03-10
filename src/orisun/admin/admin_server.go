package admin

import (
	// "bytes"
	"encoding/json"
	"errors"
	"fmt"
	// "time"

	// "fmt"
	"html/template"
	"net/http"
	pb "orisun/src/orisun/eventstore"
	l "orisun/src/orisun/logging"
	"strings"

	"orisun/src/orisun/admin/templates"

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

	// Add debug logging
	for _, t := range tmpl.Templates() {
		logger.Infof("Loaded template: %s", t.Name())
	}

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
		r.Get("/dashboard", withAuthentication(server.handleDashboard))
		r.Get("/login", server.handleLoginPage)
		r.Post("/login", server.handleLogin)

		// Existing routes
		r.Get("/users", withAuthentication(server.handleUsers))
		r.Post("/users", withAuthentication(server.handleCreateUser))
		r.Get("/users/add", withAuthentication(server.handleCreateUserPage))
		// r.Get("/users/list", withAuthentication(server.handleUsersList))
		r.Delete("/users/{username}", withAuthentication(server.handleUserDelete))
	})

	return server, nil
}

func (s *AdminServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
	templates.Dashboard(r.URL.Path).Render(r.Context(), w)
}

func (s *AdminServer) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	err := templates.Login().Render(r.Context(), w)

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

	// Validate credentials
	user, err := s.adminCommandHandlers.login(store.Username, store.Password)
	if err != nil {
		sse := datastar.NewSSE(w, r)
		sse.RemoveFragments("message")
		sse.MergeFragments(`<div id="message">` + `Login Failed` + `</div>`)
		return
	}

	userAsString, err := json.Marshal(user)
	if err != nil {
		sse := datastar.NewSSE(w, r)
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

	sse := datastar.NewSSE(w, r)

	sse.MergeFragments(`<div id="message">` + `Login Succeded` + `</div>`)

	// Redirect to users page after successful login
	sse.Redirect("/admin/dashboard")
}

func (s *AdminServer) handleCreateUserPage(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	sse.MergeFragmentTempl(templates.AddUser(r.URL.Path), datastar.WithMergeMode(datastar.FragmentMergeModeOuter))
	// sse.ExecuteScript("document.querySelector('#add-user-dialog').show()")
}

func (s *AdminServer) handleUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.adminCommandHandlers.listUsers()
	if err != nil {
		s.logger.Debugf("Failed to list users: %v", err)
		http.Error(w, "Failed to list users", http.StatusInternalServerError)
		return
	}

	// Convert internal user type to template user type
	templateUsers := make([]templates.User, len(users))
	for i, user := range users {
		// Convert []Role to []string for template compatibility
		roles := make([]string, len(user.Roles))
		for j, role := range user.Roles {
			roles[j] = string(role)
		}

		templateUsers[i] = templates.User{
			Username: user.Username,
			Roles:    roles,
		}
	}

	currentUser := getCurrentUser(r)
	templates.Users(templateUsers, currentUser, r.URL.Path).Render(r.Context(), w)
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

func withAuthentication(call func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check for authentication token
		c, err := r.Cookie("auth")
		if err != nil {
			fmt.Errorf("Template execution error: %v", err)
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		fmt.Errorf("Cookie is : %v", c)

		call(w, r)
	}
}

func getCurrentUser(r *http.Request) string {
	cookie, err := r.Cookie("auth")
	if err != nil {
		return ""
	}
	var user User
	if err := json.Unmarshal([]byte(cookie.Value), &user); err != nil {
		return ""
	}
	return user.Username
}
