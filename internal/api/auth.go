package api

import (
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

// authMiddleware wraps the server mux and enforces Subsonic credential auth.
// Auth is on by default. Set PREAMP_NO_AUTH=1 to explicitly disable (dev only).
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AuthDisabled {
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

		// Token auth: t=md5(password+salt), s=salt
		if token := r.FormValue("t"); token != "" {
			salt := r.FormValue("s")
			expected := md5Hex(password + salt)
			if !strings.EqualFold(token, expected) {
				writeError(w, r, 40, "wrong username or password")
				return
			}
			next.ServeHTTP(w, r)
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
			next.ServeHTTP(w, r)
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

