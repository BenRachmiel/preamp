package api

import (
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/BenRachmiel/preamp/internal/auth"
)

const (
	authFailureLimit    = 10
	authFailureWindow   = 5 * time.Minute
)

type failureEntry struct {
	count   atomic.Int32
	resetAt atomic.Int64 // unix nano
}

func (s *Server) checkRateLimit(r *http.Request) bool {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		ip = r.RemoteAddr
	}
	val, _ := s.authFailures.LoadOrStore(ip, &failureEntry{})
	entry := val.(*failureEntry)

	resetAt := time.Unix(0, entry.resetAt.Load())
	if time.Now().After(resetAt) {
		entry.count.Store(0)
		entry.resetAt.Store(time.Now().Add(authFailureWindow).UnixNano())
	}
	return entry.count.Load() >= int32(authFailureLimit)
}

func (s *Server) recordAuthFailure(r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		ip = r.RemoteAddr
	}
	val, _ := s.authFailures.LoadOrStore(ip, &failureEntry{})
	entry := val.(*failureEntry)

	resetAt := time.Unix(0, entry.resetAt.Load())
	if time.Now().After(resetAt) {
		entry.count.Store(1)
		entry.resetAt.Store(time.Now().Add(authFailureWindow).UnixNano())
		return
	}
	entry.count.Add(1)
}

func (s *Server) clearAuthFailure(r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		ip = r.RemoteAddr
	}
	s.authFailures.Delete(ip)
}

type contextKey string

const usernameKey contextKey = "username"

// usernameFromRequest returns the authenticated username from context,
// falling back to the "u" query param (for auth-disabled mode).
func usernameFromRequest(r *http.Request) string {
	if u, ok := r.Context().Value(usernameKey).(string); ok && u != "" {
		return u
	}
	return r.FormValue("u")
}

// requireUsername extracts the authenticated username or writes a Subsonic
// error and returns false. Use at the top of per-user handlers.
func requireUsername(w http.ResponseWriter, r *http.Request) (string, bool) {
	u := usernameFromRequest(r)
	if u == "" {
		writeError(w, r, 10, "missing parameter: u")
		return "", false
	}
	return u, true
}

// credential holds a row from the credential table.
type credential struct {
	id                string
	username          string
	hashedAPIKey      []byte
	encryptedPassword []byte
	legacyAuth        bool
}

// authMiddleware wraps the server mux and enforces Subsonic credential auth.
// Auth priority: apiKey → t+s (token) → p (legacy). First present wins.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AuthDisabled {
			if u := r.FormValue("u"); u != "" {
				r = r.WithContext(context.WithValue(r.Context(), usernameKey, u))
			}
			next.ServeHTTP(w, r)
			return
		}

		if s.checkRateLimit(r) {
			writeError(w, r, 40, "too many failed attempts")
			return
		}

		// apiKey auth: no u= required, checked against all non-expired credentials.
		if apiKey := r.FormValue("apiKey"); apiKey != "" {
			username, err := s.authenticateAPIKey(apiKey)
			if err != nil {
				s.recordAuthFailure(r)
				writeError(w, r, 40, "wrong username or password")
				return
			}
			s.clearAuthFailure(r)
			r = r.WithContext(context.WithValue(r.Context(), usernameKey, username))
			next.ServeHTTP(w, r)
			return
		}

		username := r.FormValue("u")
		if username == "" {
			writeError(w, r, 10, "missing parameter: u")
			return
		}

		authedReq := r.WithContext(context.WithValue(r.Context(), usernameKey, username))

		// Token auth: t=md5(password+salt), s=salt
		if token := r.FormValue("t"); token != "" {
			salt := r.FormValue("s")
			if err := s.authenticateToken(username, token, salt); err != nil {
				s.recordAuthFailure(r)
				writeError(w, r, 40, "wrong username or password")
				return
			}
			s.clearAuthFailure(r)
			next.ServeHTTP(w, authedReq)
			return
		}

		// Legacy auth: p=password or p=enc:hexpassword
		if p := r.FormValue("p"); p != "" {
			plain := p
			if strings.HasPrefix(p, "enc:") {
				decoded, err := hex.DecodeString(p[4:])
				if err != nil {
					s.recordAuthFailure(r)
					writeError(w, r, 40, "wrong username or password")
					return
				}
				plain = string(decoded)
			}
			if err := s.authenticateLegacy(username, plain); err != nil {
				s.recordAuthFailure(r)
				writeError(w, r, 40, "wrong username or password")
				return
			}
			s.clearAuthFailure(r)
			next.ServeHTTP(w, authedReq)
			return
		}

		writeError(w, r, 10, "missing parameter: authentication")
	})
}

// authenticateAPIKey checks the provided key against all non-expired credentials
// using bcrypt comparison. Returns the username from the matching credential.
func (s *Server) authenticateAPIKey(apiKey string) (string, error) {
	creds, err := s.loadAllCredentials()
	if err != nil {
		return "", err
	}
	for _, c := range creds {
		if bcrypt.CompareHashAndPassword(c.hashedAPIKey, []byte(apiKey)) == nil {
			return c.username, nil
		}
	}
	return "", fmt.Errorf("no matching credential")
}

// authenticateToken checks token auth against legacy-enabled credentials for the user.
func (s *Server) authenticateToken(username, token, salt string) error {
	creds, err := s.loadLegacyCredentials(username)
	if err != nil {
		return err
	}
	for _, c := range creds {
		password, err := auth.DecryptPassword(s.cfg.EncryptionKey, c.encryptedPassword)
		if err != nil {
			continue
		}
		expected := md5Hex(password + salt)
		if subtle.ConstantTimeCompare([]byte(strings.ToLower(token)), []byte(strings.ToLower(expected))) == 1 {
			return nil
		}
	}
	return fmt.Errorf("no matching credential")
}

// authenticateLegacy checks plaintext password against legacy-enabled credentials.
func (s *Server) authenticateLegacy(username, plain string) error {
	creds, err := s.loadLegacyCredentials(username)
	if err != nil {
		return err
	}
	for _, c := range creds {
		password, err := auth.DecryptPassword(s.cfg.EncryptionKey, c.encryptedPassword)
		if err != nil {
			continue
		}
		if plain == password {
			return nil
		}
	}
	return fmt.Errorf("no matching credential")
}

// loadAllCredentials loads all non-expired credentials (for apiKey auth).
func (s *Server) loadAllCredentials() ([]credential, error) {
	conn, put, err := s.db.ReadConn()
	if err != nil {
		return nil, err
	}
	defer put()

	var creds []credential
	err = sqlitex.ExecuteTransient(conn,
		`SELECT id, username, hashed_api_key FROM credential
		 WHERE expires_at IS NULL OR expires_at > strftime('%Y-%m-%dT%H:%M:%S', 'now')`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				n := stmt.ColumnLen(2)
				hash := make([]byte, n)
				stmt.ColumnBytes(2, hash)
				creds = append(creds, credential{
					id:           stmt.ColumnText(0),
					username:     stmt.ColumnText(1),
					hashedAPIKey: hash,
				})
				return nil
			},
		})
	if err != nil {
		return nil, err
	}
	if len(creds) == 0 {
		return nil, fmt.Errorf("no credentials found")
	}
	return creds, nil
}

// loadLegacyCredentials loads non-expired, legacy-auth-enabled credentials for a user.
func (s *Server) loadLegacyCredentials(username string) ([]credential, error) {
	conn, put, err := s.db.ReadConn()
	if err != nil {
		return nil, err
	}
	defer put()

	var creds []credential
	err = sqlitex.ExecuteTransient(conn,
		`SELECT id, encrypted_password FROM credential
		 WHERE username = ? AND legacy_auth = 1 AND encrypted_password IS NOT NULL
		   AND (expires_at IS NULL OR expires_at > strftime('%Y-%m-%dT%H:%M:%S', 'now'))`,
		&sqlitex.ExecOptions{
			Args: []any{username},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				n := stmt.ColumnLen(1)
				enc := make([]byte, n)
				stmt.ColumnBytes(1, enc)
				creds = append(creds, credential{
					id:                stmt.ColumnText(0),
					encryptedPassword: enc,
				})
				return nil
			},
		})
	if err != nil {
		return nil, err
	}
	if len(creds) == 0 {
		return nil, fmt.Errorf("no legacy credentials for user %q", username)
	}
	return creds, nil
}

func md5Hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}
