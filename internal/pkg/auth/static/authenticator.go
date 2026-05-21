package static

import (
	"crypto/subtle"

	"goftp/internal/pkg/auth"
)

var _ auth.Authenticator = (*Authenticator)(nil)

// Authenticator authenticates against a single configured username and password.
type Authenticator struct {
	Username string
	Password string
}

func NewStaticAuthenticator(username, password string) *Authenticator {
	return &Authenticator{
		Username: username,
		Password: password,
	}
}

func (a Authenticator) Authenticate(username, password string) bool {
	if a.Username != "" && username != a.Username {
		return false
	}

	if a.Password == "" {
		return true
	}

	return subtle.ConstantTimeCompare([]byte(password), []byte(a.Password)) == 1
}
