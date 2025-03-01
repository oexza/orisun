package admin

// Event types
const (
	EventTypeUserCreated     = "$UserCreated"
	EventTypeUserDeleted     = "$UserDeleted"
	EventTypeRolesChanged    = "$RolesChanged"
	EventTypePasswordChanged = "$PasswordChanged"
)

type UserCreated struct {
	UserId       string   `json:"user_id"`
	Username     string   `json:"username"`
	Roles        []Role `json:"roles,omitempty"`
	PasswordHash string   `json:"password_hash,omitempty"`
}

type UserDeleted struct {
	UserId string `json:"username"`
}

type UserRolesChanged struct {
	UserId string   `json:"username"`
	Roles    []string `json:"roles,omitempty"`
}

type UserPasswordChanged struct {
	UserId     string `json:"username"`
	PasswordHash string `json:"password_hash,omitempty"`
}
