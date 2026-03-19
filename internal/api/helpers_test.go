package api

import (
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/BenRachmiel/preamp/internal/auth"
	"github.com/BenRachmiel/preamp/internal/config"
	"github.com/BenRachmiel/preamp/internal/db"
)

type testServerOpts struct {
	encryptionKey  string // if set, auth is enabled
	collectorToken string // if set, enables /admin/playhistory
}

// testServer sets up a Server with an in-memory-like temp DB and returns it + cleanup.
// By default auth is disabled. Pass opts to enable auth.
func testServer(t *testing.T, opts ...testServerOpts) *Server {
	t.Helper()
	tmpDir := t.TempDir()

	cfg := &config.Config{
		ListenAddr:   ":0",
		MusicDir:     tmpDir,
		DataDir:      tmpDir,
		CoverArtDir:  tmpDir + "/covers",
		DBPath:       tmpDir + "/test.db",
		AuthDisabled: true,
	}

	if len(opts) > 0 {
		if opts[0].encryptionKey != "" {
			cfg.AuthDisabled = false
			cfg.EncryptionKey = opts[0].encryptionKey
		}
		cfg.CollectorToken = opts[0].collectorToken
	}

	os.MkdirAll(cfg.CoverArtDir, 0o755)

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return NewServer(cfg, database, log)
}

// seedData inserts test artists, albums, and songs into the database.
func seedData(t *testing.T, srv *Server) {
	t.Helper()
	conn, put, err := srv.db.WriteConn()
	if err != nil {
		t.Fatalf("WriteConn: %v", err)
	}
	defer put()

	stmts := []string{
		`INSERT INTO artist (id, name) VALUES ('art1', 'ABBA')`,
		`INSERT INTO artist (id, name) VALUES ('art2', 'Weezer')`,
		`INSERT INTO album (id, artist_id, name, year, genre, song_count, duration)
		 VALUES ('alb1', 'art1', 'Gold', 1992, 'Pop', 2, 480)`,
		`INSERT INTO album (id, artist_id, name, year, genre, song_count, duration)
		 VALUES ('alb2', 'art2', 'Blue Album', 1994, 'Rock', 1, 300)`,
		`INSERT INTO song (id, album_id, artist_id, title, track, year, genre, duration, size, suffix, bitrate, content_type, path)
		 VALUES ('s1', 'alb1', 'art1', 'Dancing Queen', 1, 1976, 'Pop', 231, 9247567, 'mp3', 320, 'audio/mpeg', '/fake/dq.mp3')`,
		`INSERT INTO song (id, album_id, artist_id, title, track, year, genre, duration, size, suffix, bitrate, content_type, path)
		 VALUES ('s2', 'alb1', 'art1', 'Waterloo', 2, 1974, 'Pop', 249, 9900000, 'mp3', 320, 'audio/mpeg', '/fake/wl.mp3')`,
		`INSERT INTO song (id, album_id, artist_id, title, track, year, genre, duration, size, suffix, bitrate, content_type, path)
		 VALUES ('s3', 'alb2', 'art2', 'Buddy Holly', 1, 1994, 'Rock', 300, 12000000, 'mp3', 320, 'audio/mpeg', '/fake/bh.mp3')`,
		// Populate FTS.
		`INSERT INTO song_fts(rowid, title, artist_name, album_name)
		 SELECT s.rowid, s.title, a.name, al.name
		 FROM song s JOIN artist a ON a.id = s.artist_id JOIN album al ON al.id = s.album_id`,
	}
	for _, stmt := range stmts {
		if err := sqlitex.ExecuteTransient(conn, stmt, nil); err != nil {
			t.Fatalf("seed %q: %v", stmt[:40], err)
		}
	}
}

type subsonicJSON struct {
	Response json.RawMessage `json:"subsonic-response"`
}

func get(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func getJSON(t *testing.T, srv *Server, path string) map[string]any {
	t.Helper()
	w := get(t, srv, path+"&f=json")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d for %s", w.Code, path)
	}

	var wrapper subsonicJSON
	if err := json.Unmarshal(w.Body.Bytes(), &wrapper); err != nil {
		t.Fatalf("unmarshal wrapper: %v\nbody: %s", err, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(wrapper.Response, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

// seedDataWithFiles creates real files on disk and matching DB rows.
// Returns the song file path and its content.
func seedDataWithFiles(t *testing.T, srv *Server) (songPath string, songContent []byte) {
	t.Helper()
	conn, put, err := srv.db.WriteConn()
	if err != nil {
		t.Fatalf("WriteConn: %v", err)
	}
	defer put()

	// Create a real audio file.
	songContent = []byte("fake-audio-content-for-testing")
	songPath = filepath.Join(srv.cfg.MusicDir, "test.mp3")
	if err := os.WriteFile(songPath, songContent, 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	// Create a cover art file.
	coverPath := filepath.Join(srv.cfg.CoverArtDir, "alb1.jpg")
	if err := os.WriteFile(coverPath, []byte("fake-jpeg-data"), 0o644); err != nil {
		t.Fatalf("writing cover art: %v", err)
	}

	err = sqlitex.ExecuteTransient(conn,
		`INSERT INTO artist (id, name) VALUES ('art1', 'ABBA')`, nil)
	if err != nil {
		t.Fatalf("seed artist: %v", err)
	}

	err = sqlitex.ExecuteTransient(conn,
		`INSERT INTO album (id, artist_id, name, year, genre, song_count, duration, cover_art)
		 VALUES ('alb1', 'art1', 'Gold', 1992, 'Pop', 1, 231, ?)`,
		&sqlitex.ExecOptions{Args: []any{coverPath}})
	if err != nil {
		t.Fatalf("seed album: %v", err)
	}

	// Insert song with parameterized path.
	err = sqlitex.ExecuteTransient(conn,
		`INSERT INTO song (id, album_id, artist_id, title, track, year, genre,
		  duration, size, suffix, bitrate, content_type, path)
		 VALUES ('s1', 'alb1', 'art1', 'Dancing Queen', 1, 1976, 'Pop',
		  231, ?, 'mp3', 320, 'audio/mpeg', ?)`,
		&sqlitex.ExecOptions{Args: []any{len(songContent), songPath}})
	if err != nil {
		t.Fatalf("seed song: %v", err)
	}

	return songPath, songContent
}

// writeTestJPEG creates a 200x200 JPEG at the given path.
func writeTestJPEG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for y := range 200 {
		for x := range 200 {
			img.Set(x, y, color.RGBA{R: 100, G: 150, B: 200, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating test JPEG: %v", err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, nil); err != nil {
		t.Fatalf("encoding test JPEG: %v", err)
	}
}

func seedDataWithRealCover(t *testing.T, srv *Server) {
	t.Helper()
	conn, put, err := srv.db.WriteConn()
	if err != nil {
		t.Fatalf("WriteConn: %v", err)
	}
	defer put()

	coverPath := filepath.Join(srv.cfg.CoverArtDir, "alb1.jpg")
	writeTestJPEG(t, coverPath)

	err = sqlitex.ExecuteTransient(conn,
		`INSERT INTO artist (id, name) VALUES ('art1', 'ABBA')`, nil)
	if err != nil {
		t.Fatalf("seed artist: %v", err)
	}
	err = sqlitex.ExecuteTransient(conn,
		`INSERT INTO album (id, artist_id, name, year, genre, song_count, duration, cover_art)
		 VALUES ('alb1', 'art1', 'Gold', 1992, 'Pop', 1, 231, ?)`,
		&sqlitex.ExecOptions{Args: []any{coverPath}})
	if err != nil {
		t.Fatalf("seed album: %v", err)
	}
}

const testEncryptionKey = "0123456789abcdef0123456789abcdef" // 32 hex chars = 16 bytes

// testServerWithAuth sets up a server with auth enabled and a seeded legacy credential.
func testServerWithAuth(t *testing.T, username, password string) *Server {
	t.Helper()
	srv := testServer(t, testServerOpts{encryptionKey: testEncryptionKey})
	seedCredential(t, srv, username, password, true)
	return srv
}

// seedCredential inserts a credential into the DB. If legacy is true, the credential
// supports token/password auth (encrypted_password populated, legacy_auth=1).
func seedCredential(t *testing.T, srv *Server, username, password string, legacy bool) string {
	t.Helper()

	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}

	var encrypted []byte
	legacyFlag := 0
	if legacy {
		encrypted, err = auth.EncryptPassword(testEncryptionKey, password)
		if err != nil {
			t.Fatalf("EncryptPassword: %v", err)
		}
		legacyFlag = 1
	}

	id := db.NewID()
	conn, put, err := srv.db.WriteConn()
	if err != nil {
		t.Fatalf("WriteConn: %v", err)
	}
	err = sqlitex.ExecuteTransient(conn,
		`INSERT INTO credential (id, username, hashed_api_key, encrypted_password, client_name, legacy_auth)
		 VALUES (?, ?, ?, ?, 'test', ?)`,
		&sqlitex.ExecOptions{Args: []any{id, username, hashed, encrypted, legacyFlag}})
	put()
	if err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	return id
}
