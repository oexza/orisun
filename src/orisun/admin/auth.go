package admin

import (
	"fmt"
)

type Role string

const (
	RoleAdmin      Role = "Admin"
	RoleOperations Role = "Operations"
	RoleRead       Role = "Read"
	RoleWrite      Role = "Write"
)

type User struct {
	Id             string
	Username       string
	HashedPassword string
	Roles          []Role
}

type Authenticator struct {
	db DB
}

func NewAuthenticator(db DB) *Authenticator {
	return &Authenticator{
		db: db,
	}
}

func (a *Authenticator) ValidateCredentials(username string, password string) (User, error) {
	user, err := a.db.GetUserByUsername(username)

	if err != nil {
		return User{}, fmt.Errorf("user not found")
	}

	if err != nil {
		return User{}, fmt.Errorf("failed to hash password")
	}

	if err = ComparePassword(user.HashedPassword, password); err != nil {
		return User{}, fmt.Errorf("invalid credentials")
	}
	return user, nil
}

func (a *Authenticator) HasRole(user User, requiredRole Role) bool {
	for _, role := range user.Roles {
		if role == requiredRole || role == RoleAdmin {
			return true
		}
	}
	return false
}
