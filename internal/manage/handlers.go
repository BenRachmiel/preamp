package manage

import (
	"crypto/rand"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"math/big"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/BenRachmiel/preamp/internal/auth"
	"github.com/BenRachmiel/preamp/internal/config"
	"github.com/BenRachmiel/preamp/internal/db"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// ManageServer handles the management UI for credential CRUD.
type ManageServer struct {
	db       *db.DB
	cfg      *config.Config
	sessions *SessionStore
	auth     Authenticator
	log      *slog.Logger
	tpl      map[string]*template.Template // per-page template sets
	mux      *http.ServeMux
}

// pageTpl parses the layout + a specific page template (plus partials).
func pageTpl(pages ...string) *template.Template {
	files := []string{"templates/layout.html", "templates/credential_row.html"}
	files = append(files, pages...)
	return template.Must(template.ParseFS(templateFS, files...))
}

// NewServer creates a new management UI server.
func NewServer(database *db.DB, cfg *config.Config, authenticator Authenticator, log *slog.Logger) *ManageServer {
	tpls := map[string]*template.Template{
		"login":              pageTpl("templates/login.html"),
		"dashboard":          pageTpl("templates/dashboard.html"),
		"credential_created": pageTpl("templates/credential_created.html"),
	}

	// Give the secret authenticator access to the login template.
	if sa, ok := authenticator.(*SecretAuthenticator); ok {
		sa.SetLoginTemplate(tpls["login"])
	}

	s := &ManageServer{
		db:       database,
		cfg:      cfg,
		sessions: NewSessionStore(),
		auth:     authenticator,
		log:      log,
		tpl:      tpls,
		mux:      http.NewServeMux(),
	}
	s.routes()
	return s
}

// Close releases resources.
func (s *ManageServer) Close() {
	s.sessions.Close()
	if c, ok := s.auth.(interface{ Close() }); ok {
		c.Close()
	}
}

// Handler returns the HTTP handler for the management UI.
func (s *ManageServer) Handler() http.Handler {
	return s.mux
}

func (s *ManageServer) routes() {
	// Static files.
	staticSub, _ := fs.Sub(staticFS, "static")
	s.mux.Handle("GET /manage/static/", http.StripPrefix("/manage/static/",
		http.FileServerFS(staticSub)))

	// Public routes.
	s.mux.HandleFunc("GET /manage/login", s.handleLoginPage)
	s.mux.HandleFunc("POST /manage/login", s.handleLoginSubmit)
	s.mux.HandleFunc("GET /manage/callback", s.handleCallback)

	// Authenticated routes.
	s.mux.HandleFunc("POST /manage/logout", s.requireSession(s.handleLogout))
	s.mux.HandleFunc("GET /manage/", s.requireSession(s.handleDashboard))
	s.mux.HandleFunc("GET /manage", s.redirectToDashboard)
	s.mux.HandleFunc("POST /manage/credentials", s.requireSession(s.handleMintCredential))
	s.mux.HandleFunc("POST /manage/credentials/{id}/renew", s.requireSession(s.handleRenewCredential))
	s.mux.HandleFunc("DELETE /manage/credentials/{id}", s.requireSession(s.handleRevokeCredential))
}

func (s *ManageServer) redirectToDashboard(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/manage/", http.StatusFound)
}

// requireSession wraps a handler to enforce an authenticated session.
func (s *ManageServer) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.sessions.Get(r); !ok {
			http.Redirect(w, r, "/manage/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

func (s *ManageServer) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	s.auth.LoginHandler(w, r)
}

func (s *ManageServer) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	username, err := s.auth.CallbackHandler(w, r)
	if err != nil {
		s.log.Warn("login failed", "err", err)
		w.WriteHeader(http.StatusUnauthorized)
		s.renderLogin(w, "Invalid username or password")
		return
	}

	_, cookie := s.sessions.Create(username)
	http.SetCookie(w, cookie)
	http.Redirect(w, r, "/manage/", http.StatusFound)
}

func (s *ManageServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	username, err := s.auth.CallbackHandler(w, r)
	if err != nil {
		s.log.Error("OIDC callback failed", "err", err)
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}

	_, cookie := s.sessions.Create(username)
	http.SetCookie(w, cookie)
	http.Redirect(w, r, "/manage/", http.StatusFound)
}

func (s *ManageServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	if sess, ok := s.sessions.Get(r); ok {
		s.sessions.Delete(sess.ID)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Path:     "/manage/",
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/manage/login", http.StatusFound)
}

type credentialView struct {
	ID         string
	ClientName string
	LegacyAuth bool
	CreatedAt  string
	ExpiresAt  string
	Expired    bool
}

func (s *ManageServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.sessions.Get(r)
	creds, err := s.listCredentials(sess.Username)
	if err != nil {
		s.log.Error("listing credentials", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := map[string]any{
		"Credentials": creds,
	}
	s.render(w, "dashboard", data)
}

func (s *ManageServer) handleMintCredential(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.sessions.Get(r)
	clientName := r.FormValue("client_name")
	legacyAuth := r.FormValue("legacy_auth") == "1"

	secret := randomBase62(32)

	hashed, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		s.log.Error("bcrypt hash", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var encrypted []byte
	legacyFlag := 0
	if legacyAuth {
		encrypted, err = auth.EncryptPassword(s.cfg.EncryptionKey, secret)
		if err != nil {
			s.log.Error("encrypting password", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		legacyFlag = 1
	}

	id := db.NewID()
	var expiresAt any
	ttl := s.cfg.CredentialTTL
	if ttlStr := r.FormValue("ttl"); ttlStr != "" && ttlStr != "0" {
		if parsed, err := time.ParseDuration(ttlStr); err == nil {
			ttl = parsed
		}
	} else if ttlStr == "0" {
		ttl = 0
	}
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl).Format("2006-01-02T15:04:05")
	}

	conn, put, err := s.db.WriteConn()
	if err != nil {
		s.log.Error("WriteConn", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	err = sqlitex.ExecuteTransient(conn,
		`INSERT INTO credential (id, username, hashed_api_key, encrypted_password, client_name, legacy_auth, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		&sqlitex.ExecOptions{Args: []any{id, sess.Username, hashed, encrypted, clientName, legacyFlag, expiresAt}})
	put()
	if err != nil {
		s.log.Error("insert credential", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := map[string]any{
		"ClientName": clientName,
		"LegacyAuth": legacyAuth,
		"Username":   sess.Username,
		"Secret":     secret,
	}
	s.tpl["credential_created"].ExecuteTemplate(w, "credential_created", data)
}

func (s *ManageServer) handleRenewCredential(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.sessions.Get(r)
	id := r.PathValue("id")

	newExpiry := time.Now().Add(s.cfg.CredentialTTL).Format("2006-01-02T15:04:05")

	conn, put, err := s.db.WriteConn()
	if err != nil {
		s.log.Error("WriteConn", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	err = sqlitex.ExecuteTransient(conn,
		`UPDATE credential SET expires_at = ? WHERE id = ? AND username = ?`,
		&sqlitex.ExecOptions{Args: []any{newExpiry, id, sess.Username}})
	put()
	if err != nil {
		s.log.Error("renew credential", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Return the updated row.
	cred, err := s.getCredential(id, sess.Username)
	if err != nil {
		s.log.Error("get credential after renew", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.tpl["dashboard"].ExecuteTemplate(w, "credential_row", cred)
}

func (s *ManageServer) handleRevokeCredential(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.sessions.Get(r)
	id := r.PathValue("id")

	conn, put, err := s.db.WriteConn()
	if err != nil {
		s.log.Error("WriteConn", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	err = sqlitex.ExecuteTransient(conn,
		`DELETE FROM credential WHERE id = ? AND username = ?`,
		&sqlitex.ExecOptions{Args: []any{id, sess.Username}})
	put()
	if err != nil {
		s.log.Error("revoke credential", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Return empty — HTMX will remove the row.
	w.WriteHeader(http.StatusOK)
}

// scanCredentialView maps a row from the standard 5-column credential SELECT
// (id, client_name, legacy_auth, created_at, expires_at) into a view struct.
func scanCredentialView(stmt *sqlite.Stmt) credentialView {
	expiresAt := stmt.ColumnText(4)
	expired := false
	if expiresAt != "" {
		exp, err := time.Parse("2006-01-02T15:04:05", expiresAt)
		if err == nil && time.Now().After(exp) {
			expired = true
		}
	}
	return credentialView{
		ID:         stmt.ColumnText(0),
		ClientName: stmt.ColumnText(1),
		LegacyAuth: stmt.ColumnInt(2) == 1,
		CreatedAt:  stmt.ColumnText(3),
		ExpiresAt:  expiresAt,
		Expired:    expired,
	}
}

func (s *ManageServer) listCredentials(username string) ([]credentialView, error) {
	conn, put, err := s.db.ReadConn()
	if err != nil {
		return nil, err
	}
	defer put()

	var creds []credentialView
	err = sqlitex.ExecuteTransient(conn,
		`SELECT id, client_name, legacy_auth, created_at, expires_at
		 FROM credential WHERE username = ? ORDER BY created_at DESC`,
		&sqlitex.ExecOptions{
			Args: []any{username},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				creds = append(creds, scanCredentialView(stmt))
				return nil
			},
		})
	if err != nil {
		return nil, err
	}
	if creds == nil {
		creds = []credentialView{}
	}
	return creds, nil
}

func (s *ManageServer) getCredential(id, username string) (*credentialView, error) {
	conn, put, err := s.db.ReadConn()
	if err != nil {
		return nil, err
	}
	defer put()

	var cred *credentialView
	err = sqlitex.ExecuteTransient(conn,
		`SELECT id, client_name, legacy_auth, created_at, expires_at
		 FROM credential WHERE id = ? AND username = ?`,
		&sqlitex.ExecOptions{
			Args: []any{id, username},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				cv := scanCredentialView(stmt)
				cred = &cv
				return nil
			},
		})
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return nil, fmt.Errorf("credential not found")
	}
	return cred, nil
}

func (s *ManageServer) render(w http.ResponseWriter, page string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tpl, ok := s.tpl[page]
	if !ok {
		s.log.Error("template not found", "page", page)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := tpl.ExecuteTemplate(w, "layout.html", data); err != nil {
		s.log.Error("rendering template", "page", page, "err", err)
	}
}

func (s *ManageServer) renderLogin(w http.ResponseWriter, errorMsg string) {
	data := map[string]any{"Error": errorMsg}
	s.render(w, "login", data)
}

const base62Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func randomBase62(n int) string {
	b := make([]byte, n)
	max := big.NewInt(int64(len(base62Chars)))
	for i := range n {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic("crypto/rand failed: " + err.Error())
		}
		b[i] = base62Chars[idx.Int64()]
	}
	return string(b)
}
