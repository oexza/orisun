package auth

import (
    "crypto/subtle"
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
    Username string
    Password string
    Roles    []Role
}

type Authenticator struct {
    users map[string]User
}

func NewAuthenticator(users []User) *Authenticator {
    userMap := make(map[string]User)
    for _, user := range users {
        userMap[user.Username] = user
    }
    return &Authenticator{users: userMap}
}

func (a *Authenticator) ValidateCredentials(username, password string) (User, error) {
    user, exists := a.users[username]
    if !exists {
        return User{}, fmt.Errorf("invalid credentials")
    }

    if subtle.ConstantTimeCompare([]byte(user.Password), []byte(password)) != 1 {
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