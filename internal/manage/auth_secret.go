package manage

import (
	"crypto/subtle"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strings"
)

// SecretAuthenticator authenticates management UI users against a file-mounted
// username:password credential.
type SecretAuthenticator struct {
	username string
	password string
	loginTpl *template.Template
}

// NewSecretAuthenticator reads the secret file and parses username:password.
// The file must contain exactly one line in the format "username:password".
func NewSecretAuthenticator(path string) (*SecretAuthenticator, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading admin secret file: %w", err)
	}

	line := strings.TrimSpace(string(data))
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("admin secret file must contain username:password")
	}

	return &SecretAuthenticator{
		username: parts[0],
		password: parts[1],
	}, nil
}

// SetLoginTemplate sets the template used to render the login form.
func (a *SecretAuthenticator) SetLoginTemplate(tpl *template.Template) {
	a.loginTpl = tpl
}

// LoginHandler renders the login form on GET.
func (a *SecretAuthenticator) LoginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data := map[string]any{"Error": ""}
	if a.loginTpl != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		a.loginTpl.ExecuteTemplate(w, "layout.html", data)
		return
	}
	// Fallback if no template set.
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, `<form method="POST" action="/manage/login"><input name="username" placeholder="Username"><input name="password" type="password" placeholder="Password"><button type="submit">Login</button></form>`)
}

// CallbackHandler validates the POST form credentials.
func (a *SecretAuthenticator) CallbackHandler(w http.ResponseWriter, r *http.Request) (string, error) {
	username := r.FormValue("username")
	password := r.FormValue("password")

	usernameMatch := subtle.ConstantTimeCompare([]byte(username), []byte(a.username)) == 1
	passwordMatch := subtle.ConstantTimeCompare([]byte(password), []byte(a.password)) == 1

	if !usernameMatch || !passwordMatch {
		return "", fmt.Errorf("invalid credentials")
	}
	return a.username, nil
}
