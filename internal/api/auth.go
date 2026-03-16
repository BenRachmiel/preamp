package api

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/BenRachmiel/preamp/internal/auth"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

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

// authMiddleware wraps the server mux and enforces Subsonic credential auth.
// Auth is on by default. Set PREAMP_NO_AUTH=1 to explicitly disable (dev only).
// Anonymous endpoints (ping, getLicense, etc.) work without u=.
// Per-user endpoints must call usernameFromRequest and validate non-empty.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	if s.cfg.AuthDisabled {
		s.log.Warn("auth disabled — all requests accepted without credential check")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AuthDisabled {
			if u := r.FormValue("u"); u != "" {
				r = r.WithContext(context.WithValue(r.Context(), usernameKey, u))
			}
			next.ServeHTTP(w, r)
			return
		}

		username := r.FormValue("u")
		if username == "" {
			writeError(w, r, 10, "missing parameter: u")
			return
		}

		password, err := s.lookupPassword(username)
		if err != nil {
			writeError(w, r, 40, "wrong username or password")
			return
		}

		authedReq := r.WithContext(context.WithValue(r.Context(), usernameKey, username))

		// Token auth: t=md5(password+salt), s=salt
		if token := r.FormValue("t"); token != "" {
			salt := r.FormValue("s")
			expected := md5Hex(password + salt)
			if !strings.EqualFold(token, expected) {
				writeError(w, r, 40, "wrong username or password")
				return
			}
			next.ServeHTTP(w, authedReq)
			return
		}

		// Legacy auth: p=password or p=enc:hexpassword
		if p := r.FormValue("p"); p != "" {
			plain := p
			if strings.HasPrefix(p, "enc:") {
				decoded, err := hex.DecodeString(p[4:])
				if err != nil {
					writeError(w, r, 40, "wrong username or password")
					return
				}
				plain = string(decoded)
			}
			if plain != password {
				writeError(w, r, 40, "wrong username or password")
				return
			}
			next.ServeHTTP(w, authedReq)
			return
		}

		writeError(w, r, 10, "missing parameter: authentication")
	})
}

// lookupPassword finds a credential by username, decrypts the password,
// and checks expiry.
func (s *Server) lookupPassword(username string) (string, error) {
	conn, put, err := s.db.ReadConn()
	if err != nil {
		return "", err
	}
	defer put()

	var encryptedPassword []byte
	var expiresAt string
	found := false

	err = sqlitex.ExecuteTransient(conn,
		`SELECT encrypted_password, expires_at FROM credential WHERE username = ?`,
		&sqlitex.ExecOptions{
			Args: []any{username},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				n := stmt.ColumnLen(0)
				encryptedPassword = make([]byte, n)
				stmt.ColumnBytes(0, encryptedPassword)
				expiresAt = stmt.ColumnText(1)
				found = true
				return nil
			},
		})
	if err != nil || !found {
		return "", fmt.Errorf("credential not found")
	}

	if expiresAt != "" {
		exp, err := time.Parse("2006-01-02T15:04:05", expiresAt)
		if err == nil && time.Now().After(exp) {
			return "", fmt.Errorf("credential expired")
		}
	}

	password, err := auth.DecryptPassword(s.cfg.EncryptionKey, encryptedPassword)
	if err != nil {
		return "", fmt.Errorf("decrypting password: %w", err)
	}

	return password, nil
}

func md5Hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

