package manage

import "net/http"

// Authenticator defines how the management UI authenticates users.
// Exactly one implementation is active per deployment.
type Authenticator interface {
	// LoginHandler serves the login page (GET) or initiates the auth flow (e.g. OIDC redirect).
	LoginHandler(w http.ResponseWriter, r *http.Request)

	// CallbackHandler processes the auth response (form POST or OIDC callback).
	// Returns the authenticated username on success.
	CallbackHandler(w http.ResponseWriter, r *http.Request) (username string, err error)
}
