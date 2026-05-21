package auth

// Authenticator validates FTP login credentials.
type Authenticator interface {
	Authenticate(username, password string) bool
}
