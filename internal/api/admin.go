package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/BenRachmiel/preamp/internal/auth"
	"github.com/BenRachmiel/preamp/internal/db"
)

// adminCredential is the JSON representation of a credential for the admin API.
type adminCredential struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	ClientName string `json:"client_name"`
	LegacyAuth bool   `json:"legacy_auth"`
	CreatedAt  string `json:"created_at"`
	ExpiresAt  string `json:"expires_at,omitempty"`
	Expired    bool   `json:"expired"`
	Secret     string `json:"secret,omitempty"`
}

type adminStats struct {
	Artists             int `json:"artists"`
	Albums              int `json:"albums"`
	Songs               int `json:"songs"`
	AlbumsMissingArt    int `json:"albums_missing_art"`
	SongsUnknownArtist  int `json:"songs_unknown_artist"`
	SongsNoGenre        int `json:"songs_no_genre"`
	SongsNoYear         int `json:"songs_no_year"`
	SongsZeroDuration   int `json:"songs_zero_duration"`
	AlbumsNoYear        int `json:"albums_no_year"`
	AlbumsNoGenre       int `json:"albums_no_genre"`
}

type createCredentialRequest struct {
	ClientName string `json:"client_name"`
	LegacyAuth bool   `json:"legacy_auth"`
	TTL        string `json:"ttl"`
}

// adminUsername extracts the username set by adminAuthMiddleware.
func adminUsername(r *http.Request) string {
	if u, ok := r.Context().Value(usernameKey).(string); ok {
		return u
	}
	return ""
}

func (s *Server) handleAdminWhoami(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"username": adminUsername(r)})
}

func (s *Server) handleAdminListCredentials(w http.ResponseWriter, r *http.Request) {
	username := adminUsername(r)

	conn, put, err := s.db.ReadConn()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}
	defer put()

	creds := []adminCredential{}
	err = sqlitex.ExecuteTransient(conn,
		`SELECT id, username, client_name, legacy_auth, created_at, expires_at
		 FROM credential WHERE username = ? ORDER BY created_at DESC`,
		&sqlitex.ExecOptions{
			Args: []any{username},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				creds = append(creds, scanAdminCredential(stmt))
				return nil
			},
		})
	if err != nil {
		s.log.Error("list credentials", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}

	writeJSON(w, http.StatusOK, creds)
}

func (s *Server) handleAdminCreateCredential(w http.ResponseWriter, r *http.Request) {
	username := adminUsername(r)

	var req createCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	secret := randomBase62(32)

	hashed, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		s.log.Error("bcrypt hash", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	var encrypted []byte
	legacyFlag := 0
	if req.LegacyAuth {
		encrypted, err = auth.EncryptPassword(s.cfg.EncryptionKey, secret)
		if err != nil {
			s.log.Error("encrypting password", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		legacyFlag = 1
	}

	id := db.NewID()
	var expiresAt any
	ttl := s.cfg.CredentialTTL
	if req.TTL != "" && req.TTL != "0" {
		if parsed, err := time.ParseDuration(req.TTL); err == nil {
			ttl = parsed
		}
	} else if req.TTL == "0" {
		ttl = 0
	}
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl).Format("2006-01-02T15:04:05")
	}

	conn, put, err := s.db.WriteConn()
	if err != nil {
		s.log.Error("WriteConn", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	err = sqlitex.ExecuteTransient(conn,
		`INSERT INTO credential (id, username, hashed_api_key, encrypted_password, client_name, legacy_auth, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		&sqlitex.ExecOptions{Args: []any{id, username, hashed, encrypted, req.ClientName, legacyFlag, expiresAt}})
	put()
	if err != nil {
		s.log.Error("insert credential", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	expiresAtStr := ""
	if s, ok := expiresAt.(string); ok {
		expiresAtStr = s
	}
	cred := adminCredential{
		ID:         id,
		Username:   username,
		ClientName: req.ClientName,
		LegacyAuth: req.LegacyAuth,
		CreatedAt:  time.Now().Format("2006-01-02T15:04:05"),
		ExpiresAt:  expiresAtStr,
		Secret:     secret,
	}
	writeJSON(w, http.StatusCreated, cred)
}

func (s *Server) handleAdminRenewCredential(w http.ResponseWriter, r *http.Request) {
	username := adminUsername(r)
	id := r.PathValue("id")

	newExpiry := time.Now().Add(s.cfg.CredentialTTL).Format("2006-01-02T15:04:05")

	conn, put, err := s.db.WriteConn()
	if err != nil {
		s.log.Error("WriteConn", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	var changed int
	err = sqlitex.ExecuteTransient(conn,
		`UPDATE credential SET expires_at = ? WHERE id = ? AND username = ?`,
		&sqlitex.ExecOptions{Args: []any{newExpiry, id, username}})
	changed = conn.Changes()
	put()
	if err != nil {
		s.log.Error("renew credential", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if changed == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "credential not found"})
		return
	}

	// Read back the updated credential.
	cred, err := s.getAdminCredential(id, username)
	if err != nil {
		s.log.Error("get credential after renew", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, cred)
}

func (s *Server) handleAdminDeleteCredential(w http.ResponseWriter, r *http.Request) {
	username := adminUsername(r)
	id := r.PathValue("id")

	conn, put, err := s.db.WriteConn()
	if err != nil {
		s.log.Error("WriteConn", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	err = sqlitex.ExecuteTransient(conn,
		`DELETE FROM credential WHERE id = ? AND username = ?`,
		&sqlitex.ExecOptions{Args: []any{id, username}})
	changed := conn.Changes()
	put()
	if err != nil {
		s.log.Error("delete credential", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if changed == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "credential not found"})
		return
	}

	s.flushAPIKeyCache()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	conn, put, err := s.db.ReadConn()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}
	defer put()

	var stats adminStats
	err = sqlitex.ExecuteTransient(conn,
		`SELECT
		   (SELECT COUNT(*) FROM artist) AS artists,
		   (SELECT COUNT(*) FROM album) AS albums,
		   (SELECT COUNT(*) FROM song) AS songs,
		   (SELECT COUNT(*) FROM album WHERE cover_art IS NULL OR cover_art = '') AS albums_missing_art,
		   (SELECT COUNT(*) FROM song WHERE artist_id IN (SELECT id FROM artist WHERE name = 'Unknown Artist')) AS songs_unknown_artist,
		   (SELECT COUNT(*) FROM song WHERE genre IS NULL OR genre = '') AS songs_no_genre,
		   (SELECT COUNT(*) FROM song WHERE year IS NULL OR year = 0) AS songs_no_year,
		   (SELECT COUNT(*) FROM song WHERE duration = 0) AS songs_zero_duration,
		   (SELECT COUNT(*) FROM album WHERE year IS NULL OR year = 0) AS albums_no_year,
		   (SELECT COUNT(*) FROM album WHERE genre IS NULL OR genre = '') AS albums_no_genre`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				stats.Artists = stmt.ColumnInt(0)
				stats.Albums = stmt.ColumnInt(1)
				stats.Songs = stmt.ColumnInt(2)
				stats.AlbumsMissingArt = stmt.ColumnInt(3)
				stats.SongsUnknownArtist = stmt.ColumnInt(4)
				stats.SongsNoGenre = stmt.ColumnInt(5)
				stats.SongsNoYear = stmt.ColumnInt(6)
				stats.SongsZeroDuration = stmt.ColumnInt(7)
				stats.AlbumsNoYear = stmt.ColumnInt(8)
				stats.AlbumsNoGenre = stmt.ColumnInt(9)
				return nil
			},
		})
	if err != nil {
		s.log.Error("admin stats query", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

type issueSpec struct {
	songLevel  bool
	condition  string
}

var issueTypes = map[string]issueSpec{
	"songs_no_genre":        {songLevel: true, condition: "s.genre IS NULL OR s.genre = ''"},
	"songs_no_year":         {songLevel: true, condition: "s.year IS NULL OR s.year = 0"},
	"songs_unknown_artist":  {songLevel: true, condition: "ar.name = 'Unknown Artist'"},
	"songs_zero_duration":   {songLevel: true, condition: "s.duration = 0"},
	"albums_missing_art":    {songLevel: false, condition: "al.cover_art IS NULL OR al.cover_art = ''"},
	"albums_no_year":        {songLevel: false, condition: "al.year IS NULL OR al.year = 0"},
	"albums_no_genre":       {songLevel: false, condition: "al.genre IS NULL OR al.genre = ''"},
}

func (s *Server) handleAdminIssues(w http.ResponseWriter, r *http.Request) {
	issueType := r.URL.Query().Get("type")
	spec, ok := issueTypes[issueType]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown issue type"})
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	conn, put, err := s.db.ReadConn()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}
	defer put()

	// Count total.
	var total int
	var countQuery string
	if spec.songLevel {
		countQuery = "SELECT COUNT(*) FROM song s JOIN artist ar ON ar.id = s.artist_id WHERE " + spec.condition
	} else {
		countQuery = "SELECT COUNT(*) FROM album al JOIN artist ar ON ar.id = al.artist_id WHERE " + spec.condition
	}
	err = sqlitex.ExecuteTransient(conn, countQuery, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			total = stmt.ColumnInt(0)
			return nil
		},
	})
	if err != nil {
		s.log.Error("admin issues count", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}

	prefix := s.cfg.MusicDir
	if prefix != "" && prefix[len(prefix)-1] != '/' {
		prefix += "/"
	}

	if spec.songLevel {
		type songItem struct {
			Title    string `json:"title"`
			Artist   string `json:"artist"`
			Album    string `json:"album"`
			Path     string `json:"path"`
			Genre    string `json:"genre"`
			Year     int    `json:"year"`
			Duration int    `json:"duration"`
		}
		items := []songItem{}
		err = sqlitex.ExecuteTransient(conn,
			"SELECT s.title, ar.name, al.name, s.path, s.genre, s.year, s.duration "+
				"FROM song s JOIN album al ON al.id = s.album_id JOIN artist ar ON ar.id = s.artist_id "+
				"WHERE "+spec.condition+" ORDER BY ar.name, al.name, s.track LIMIT ? OFFSET ?",
			&sqlitex.ExecOptions{
				Args: []any{limit, offset},
				ResultFunc: func(stmt *sqlite.Stmt) error {
					p := stmt.ColumnText(3)
					if strings.HasPrefix(p, prefix) {
						p = p[len(prefix):]
					}
					items = append(items, songItem{
						Title:    stmt.ColumnText(0),
						Artist:   stmt.ColumnText(1),
						Album:    stmt.ColumnText(2),
						Path:     p,
						Genre:    stmt.ColumnText(4),
						Year:     stmt.ColumnInt(5),
						Duration: stmt.ColumnInt(6),
					})
					return nil
				},
			})
		if err != nil {
			s.log.Error("admin issues query", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
	} else {
		type albumItem struct {
			Name      string `json:"name"`
			Artist    string `json:"artist"`
			Year      int    `json:"year"`
			Genre     string `json:"genre"`
			SongCount int    `json:"song_count"`
		}
		items := []albumItem{}
		err = sqlitex.ExecuteTransient(conn,
			"SELECT al.name, ar.name, al.year, al.genre, al.song_count "+
				"FROM album al JOIN artist ar ON ar.id = al.artist_id "+
				"WHERE "+spec.condition+" ORDER BY ar.name, al.name LIMIT ? OFFSET ?",
			&sqlitex.ExecOptions{
				Args: []any{limit, offset},
				ResultFunc: func(stmt *sqlite.Stmt) error {
					items = append(items, albumItem{
						Name:      stmt.ColumnText(0),
						Artist:    stmt.ColumnText(1),
						Year:      stmt.ColumnInt(2),
						Genre:     stmt.ColumnText(3),
						SongCount: stmt.ColumnInt(4),
					})
					return nil
				},
			})
		if err != nil {
			s.log.Error("admin issues query", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
	}
}

func (s *Server) handleAdminGetScanStatus(w http.ResponseWriter, r *http.Request) {
	if s.scanner == nil {
		writeJSON(w, http.StatusOK, map[string]any{"scanning": false, "count": 0})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scanning": s.scanner.Scanning(), "count": s.scanner.Count()})
}

func (s *Server) handleAdminStartScan(w http.ResponseWriter, r *http.Request) {
	if s.scanner == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "scanner not configured"})
		return
	}
	go func() {
		if err := s.scanner.Run(); err != nil {
			s.log.Error("scan failed", "err", err)
		}
	}()
	writeJSON(w, http.StatusOK, map[string]any{"scanning": true, "count": s.scanner.Count()})
}

// --- play history (collector endpoint) ---

// collectorAuth authenticates via bearer token (for the collector service) or
// falls through to the standard admin auth (Remote-User header) if no bearer token.
func (s *Server) collectorAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.CollectorToken == "" {
			http.NotFound(w, r)
			return
		}

		if hdr := r.Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
			token := hdr[len("Bearer "):]
			if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.CollectorToken)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}

		// No bearer token — fall through to admin auth (Remote-User / dev mode).
		s.adminAuthMiddleware(next).ServeHTTP(w, r)
	})
}

type playHistoryEntry struct {
	Rowid    int64  `json:"rowid"`
	PlayedAt string `json:"playedAt"`
	SongID   string `json:"songId"`
	Title    string `json:"title"`
	Duration int    `json:"duration"`
	Track    int    `json:"track"`
	Disc     int    `json:"disc"`
	Year     int    `json:"year"`
	Genre    string `json:"genre"`
	Bitrate  int    `json:"bitrate"`
	AlbumID  string `json:"albumId"`
	Album    string `json:"album"`
	ArtistID string `json:"artistId"`
	Artist   string `json:"artist"`
}

func (s *Server) handlePlayHistory(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	conn, put, err := s.db.ReadConn()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}
	defer put()

	entries := []playHistoryEntry{}
	err = sqlitex.ExecuteTransient(conn,
		`SELECT ph.rowid, ph.played_at,
		        s.id, s.title, s.duration, s.track, s.disc, s.year, s.genre, s.bitrate,
		        al.id, al.name, a.id, a.name
		 FROM play_history ph
		 JOIN song s ON s.id = ph.song_id
		 JOIN album al ON al.id = s.album_id
		 JOIN artist a ON a.id = al.artist_id
		 WHERE ph.rowid > ?
		 ORDER BY ph.rowid ASC
		 LIMIT ?`,
		&sqlitex.ExecOptions{
			Args: []any{since, limit},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				entries = append(entries, playHistoryEntry{
					Rowid:    stmt.ColumnInt64(0),
					PlayedAt: stmt.ColumnText(1),
					SongID:   stmt.ColumnText(2),
					Title:    stmt.ColumnText(3),
					Duration: stmt.ColumnInt(4),
					Track:    stmt.ColumnInt(5),
					Disc:     stmt.ColumnInt(6),
					Year:     stmt.ColumnInt(7),
					Genre:    stmt.ColumnText(8),
					Bitrate:  stmt.ColumnInt(9),
					AlbumID:  stmt.ColumnText(10),
					Album:    stmt.ColumnText(11),
					ArtistID: stmt.ColumnText(12),
					Artist:   stmt.ColumnText(13),
				})
				return nil
			},
		})
	if err != nil {
		s.log.Error("play history query", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// --- helpers ---

func scanAdminCredential(stmt *sqlite.Stmt) adminCredential {
	expiresAt := stmt.ColumnText(5)
	expired := false
	if expiresAt != "" {
		exp, err := time.Parse("2006-01-02T15:04:05", expiresAt)
		if err == nil && time.Now().After(exp) {
			expired = true
		}
	}
	return adminCredential{
		ID:         stmt.ColumnText(0),
		Username:   stmt.ColumnText(1),
		ClientName: stmt.ColumnText(2),
		LegacyAuth: stmt.ColumnInt(3) == 1,
		CreatedAt:  stmt.ColumnText(4),
		ExpiresAt:  expiresAt,
		Expired:    expired,
	}
}

func (s *Server) getAdminCredential(id, username string) (*adminCredential, error) {
	conn, put, err := s.db.ReadConn()
	if err != nil {
		return nil, err
	}
	defer put()

	var cred *adminCredential
	err = sqlitex.ExecuteTransient(conn,
		`SELECT id, username, client_name, legacy_auth, created_at, expires_at
		 FROM credential WHERE id = ? AND username = ?`,
		&sqlitex.ExecOptions{
			Args: []any{id, username},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				c := scanAdminCredential(stmt)
				cred = &c
				return nil
			},
		})
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return nil, errCredentialNotFound
	}
	return cred, nil
}

var errCredentialNotFound = errors.New("credential not found")

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
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

