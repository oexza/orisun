package admin

// Event types
const (
	EventTypeUserCreated     = "$UserCreated"
	EventTypeUserDeleted     = "$UserDeleted"
	EventTypeRolesChanged    = "$RolesChanged"
	EventTypePasswordChanged = "$PasswordChanged"
)

type UserCreated struct {
	Username     string   `json:"username"`
	Roles        []string `json:"roles,omitempty"`
	PasswordHash string   `json:"password_hash,omitempty"`
}

type UserDeleted struct {
	Username string `json:"username"`
}

type UserRolesChanged struct {
	Username string   `json:"username"`
	Roles    []string `json:"roles,omitempty"`
}

type UserPasswordChanged struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash,omitempty"`
}