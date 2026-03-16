package api

import (
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/BenRachmiel/preamp/internal/auth"
	"github.com/BenRachmiel/preamp/internal/config"
	"github.com/BenRachmiel/preamp/internal/db"
	"github.com/BenRachmiel/preamp/internal/scanner"
)

type testServerOpts struct {
	encryptionKey string // if set, auth is enabled
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

	if len(opts) > 0 && opts[0].encryptionKey != "" {
		cfg.AuthDisabled = false
		cfg.EncryptionKey = opts[0].encryptionKey
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

// --- System endpoint tests ---

func TestPingJSON(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/ping?")

	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}
	if resp["version"] != APIVersion {
		t.Errorf("version = %v, want %s", resp["version"], APIVersion)
	}
	if resp["type"] != ServerName {
		t.Errorf("type = %v, want %s", resp["type"], ServerName)
	}
}

func TestPingXML(t *testing.T) {
	srv := testServer(t)
	w := get(t, srv, "/rest/ping")

	if ct := w.Header().Get("Content-Type"); ct != "application/xml; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}

	var resp SubsonicResponse
	if err := xml.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("status = %q, want ok", resp.Status)
	}
}

func TestPingViewSuffix(t *testing.T) {
	srv := testServer(t)
	w := get(t, srv, "/rest/ping.view?f=json")
	if w.Code != http.StatusOK {
		t.Errorf("status %d for .view suffix", w.Code)
	}
}

func TestGetLicense(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getLicense?")

	license, ok := resp["license"].(map[string]any)
	if !ok {
		t.Fatalf("missing license in response")
	}
	if license["valid"] != true {
		t.Errorf("license.valid = %v, want true", license["valid"])
	}
}

func TestGetMusicFolders(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getMusicFolders?")

	mf, ok := resp["musicFolders"].(map[string]any)
	if !ok {
		t.Fatalf("missing musicFolders")
	}
	folders, ok := mf["musicFolder"].([]any)
	if !ok || len(folders) == 0 {
		t.Fatalf("missing musicFolder array")
	}
	f := folders[0].(map[string]any)
	if f["name"] != "Music" {
		t.Errorf("folder name = %v", f["name"])
	}
}

func TestGetOpenSubsonicExtensions(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getOpenSubsonicExtensions?")

	// Should be present (even if empty).
	if resp["status"] != "ok" {
		t.Errorf("status = %v", resp["status"])
	}
}

// --- Browsing endpoint tests ---

func TestGetArtists(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getArtists?")

	artists, ok := resp["artists"].(map[string]any)
	if !ok {
		t.Fatalf("missing artists")
	}
	index, ok := artists["index"].([]any)
	if !ok {
		t.Fatalf("missing index, got: %v", artists)
	}
	if len(index) != 2 {
		t.Errorf("expected 2 index entries (A, W), got %d", len(index))
	}
}

func TestGetArtistsEmptyDB(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getArtists?")

	artists := resp["artists"].(map[string]any)
	index := artists["index"].([]any)
	if len(index) != 0 {
		t.Errorf("expected empty index, got %d entries", len(index))
	}
}

func TestGetArtist(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getArtist?id=art1")

	artist, ok := resp["artist"].(map[string]any)
	if !ok {
		t.Fatalf("missing artist")
	}
	if artist["name"] != "ABBA" {
		t.Errorf("name = %v", artist["name"])
	}
	albums := artist["album"].([]any)
	if len(albums) != 1 {
		t.Errorf("expected 1 album, got %d", len(albums))
	}
}

func TestGetArtistNotFound(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getArtist?id=nonexistent")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status for missing artist")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 70 {
		t.Errorf("error code = %v, want 70", apiErr["code"])
	}
}

func TestGetArtistMissingID(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getArtist?")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestGetAlbum(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbum?id=alb1")

	album, ok := resp["album"].(map[string]any)
	if !ok {
		t.Fatalf("missing album")
	}
	if album["name"] != "Gold" {
		t.Errorf("name = %v", album["name"])
	}
	songs := album["song"].([]any)
	if len(songs) != 2 {
		t.Errorf("expected 2 songs, got %d", len(songs))
	}
	// Verify song fields.
	s := songs[0].(map[string]any)
	if s["type"] != "music" {
		t.Errorf("song type = %v, want music", s["type"])
	}
}

func TestGetSong(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getSong?id=s1")

	song, ok := resp["song"].(map[string]any)
	if !ok {
		t.Fatalf("missing song")
	}
	if song["title"] != "Dancing Queen" {
		t.Errorf("title = %v", song["title"])
	}
	if song["artist"] != "ABBA" {
		t.Errorf("artist = %v", song["artist"])
	}
}

func TestGetGenres(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getGenres?")

	genres := resp["genres"].(map[string]any)
	genreList := genres["genre"].([]any)
	if len(genreList) != 2 {
		t.Errorf("expected 2 genres (Pop, Rock), got %d", len(genreList))
	}
}

func TestGetGenresEmpty(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getGenres?")

	genres := resp["genres"].(map[string]any)
	genreList := genres["genre"].([]any)
	if len(genreList) != 0 {
		t.Errorf("expected empty genres, got %d", len(genreList))
	}
}

// --- Search tests ---

func TestSearch3FTS(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/search3?query=dancing")

	sr := resp["searchResult3"].(map[string]any)
	songs := sr["song"].([]any)
	if len(songs) != 1 {
		t.Fatalf("expected 1 song for 'dancing', got %d", len(songs))
	}
	s := songs[0].(map[string]any)
	if s["title"] != "Dancing Queen" {
		t.Errorf("title = %v", s["title"])
	}
}

func TestSearch3EmptyResult(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/search3?query=zzzznothing")

	sr := resp["searchResult3"].(map[string]any)
	// All arrays should be [] not null.
	artists := sr["artist"].([]any)
	albums := sr["album"].([]any)
	songs := sr["song"].([]any)
	if len(artists) != 0 {
		t.Errorf("artist should be empty, got %d", len(artists))
	}
	if len(albums) != 0 {
		t.Errorf("album should be empty, got %d", len(albums))
	}
	if len(songs) != 0 {
		t.Errorf("song should be empty, got %d", len(songs))
	}
}

func TestSearch3ByArtist(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/search3?query=ABBA")

	sr := resp["searchResult3"].(map[string]any)
	artists := sr["artist"].([]any)
	if len(artists) != 1 {
		t.Errorf("expected 1 artist for 'ABBA', got %d", len(artists))
	}
}

func TestSearch3MissingQuery(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/search3?")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status for missing query")
	}
}

// --- List endpoint tests ---

func TestGetAlbumList2Newest(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=newest")

	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 2 {
		t.Errorf("expected 2 albums, got %d", len(albums))
	}
}

func TestGetAlbumList2AlphabeticalByName(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=alphabeticalByName")

	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) < 2 {
		t.Fatalf("expected 2 albums, got %d", len(albums))
	}
	// Blue Album should come before Gold alphabetically.
	first := albums[0].(map[string]any)
	if first["name"] != "Blue Album" {
		t.Errorf("first album = %v, want Blue Album", first["name"])
	}
}

func TestGetAlbumList2ByGenre(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=byGenre&genre=Pop")

	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 1 {
		t.Errorf("expected 1 Pop album, got %d", len(albums))
	}
}

func TestGetAlbumList2Random(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=random&size=1")

	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 1 {
		t.Errorf("expected 1 random album, got %d", len(albums))
	}
}

func TestGetAlbumList2MissingType(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getAlbumList2?")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status for missing type")
	}
}

func TestGetAlbumList2EmptyResult(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=byGenre&genre=Metal")

	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 0 {
		t.Errorf("expected 0 albums for Metal, got %d", len(albums))
	}
}

func TestGetRandomSongs(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getRandomSongs?size=2")

	rs := resp["randomSongs"].(map[string]any)
	songs := rs["song"].([]any)
	if len(songs) != 2 {
		t.Errorf("expected 2 random songs, got %d", len(songs))
	}
}

func TestGetStarred2Empty(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getStarred2?")

	st := resp["starred2"].(map[string]any)
	if artists := st["artist"].([]any); len(artists) != 0 {
		t.Errorf("starred2.artist should be empty, got %d", len(artists))
	}
	if albums := st["album"].([]any); len(albums) != 0 {
		t.Errorf("starred2.album should be empty, got %d", len(albums))
	}
	if songs := st["song"].([]any); len(songs) != 0 {
		t.Errorf("starred2.song should be empty, got %d", len(songs))
	}
}

func TestGetSongsByGenre(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getSongsByGenre?genre=Pop")

	sg := resp["songsByGenre"].(map[string]any)
	songs := sg["song"].([]any)
	if len(songs) != 2 {
		t.Errorf("expected 2 Pop songs, got %d", len(songs))
	}
}

func TestGetSongsByGenreEmpty(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getSongsByGenre?genre=Metal")

	sg := resp["songsByGenre"].(map[string]any)
	songs := sg["song"].([]any)
	if len(songs) != 0 {
		t.Errorf("expected 0 songs for Metal, got %d", len(songs))
	}
}

// --- Scan endpoint tests ---

func TestGetScanStatusNoScanner(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getScanStatus?")

	ss := resp["scanStatus"].(map[string]any)
	if ss["scanning"] != false {
		t.Errorf("scanning = %v, want false", ss["scanning"])
	}
}

func TestStartScanNoScanner(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/startScan?")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status when scanner not configured")
	}
}

func TestStartScanHappyPath(t *testing.T) {
	srv := testServer(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sc := scanner.New(srv.db, srv.cfg.MusicDir, srv.cfg.CoverArtDir, log)
	srv.SetScanner(sc)

	resp := getJSON(t, srv, "/rest/startScan?")

	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}
	ss := resp["scanStatus"].(map[string]any)
	if ss["scanning"] != true {
		t.Errorf("scanning = %v, want true", ss["scanning"])
	}
}

func TestGetScanStatusWithScanner(t *testing.T) {
	srv := testServer(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sc := scanner.New(srv.db, srv.cfg.MusicDir, srv.cfg.CoverArtDir, log)
	srv.SetScanner(sc)

	resp := getJSON(t, srv, "/rest/getScanStatus?")

	ss := resp["scanStatus"].(map[string]any)
	if ss["scanning"] != false {
		t.Errorf("scanning = %v, want false (no scan running)", ss["scanning"])
	}
}

// --- Media endpoint tests ---

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

func TestStreamHappyPath(t *testing.T) {
	srv := testServer(t)
	_, content := seedDataWithFiles(t, srv)

	w := get(t, srv, "/rest/stream?id=s1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "audio/mpeg" {
		t.Errorf("Content-Type = %q, want audio/mpeg", ct)
	}
	if w.Body.String() != string(content) {
		t.Errorf("body mismatch: got %d bytes, want %d", w.Body.Len(), len(content))
	}
}

func TestStreamMissingID(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/stream?")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestStreamSongNotFound(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/stream?id=nonexistent")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 70 {
		t.Errorf("error code = %v, want 70", apiErr["code"])
	}
}

func TestStreamFileMissingOnDisk(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv) // uses /fake/ paths that don't exist

	resp := getJSON(t, srv, "/rest/stream?id=s1")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status for missing file")
	}
}

func TestDownloadHappyPath(t *testing.T) {
	srv := testServer(t)
	_, content := seedDataWithFiles(t, srv)

	w := get(t, srv, "/rest/download?id=s1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != string(content) {
		t.Errorf("body mismatch")
	}
}

func TestDownloadMissingID(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/download?")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestGetCoverArtHappyPath(t *testing.T) {
	srv := testServer(t)
	seedDataWithFiles(t, srv)

	w := get(t, srv, "/rest/getCoverArt?id=alb1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if w.Body.String() != "fake-jpeg-data" {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestGetCoverArtWithPrefix(t *testing.T) {
	srv := testServer(t)
	seedDataWithFiles(t, srv)

	w := get(t, srv, "/rest/getCoverArt?id=al-alb1")
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (al- prefix should be stripped)", w.Code)
	}
}

func TestGetCoverArtAlbumNotFound(t *testing.T) {
	srv := testServer(t)
	w := get(t, srv, "/rest/getCoverArt?id=nonexistent")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestGetCoverArtNoCoverSet(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv) // albums have empty cover_art

	w := get(t, srv, "/rest/getCoverArt?id=alb1")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for album with no cover", w.Code)
	}
}

func TestGetCoverArtMissingID(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getCoverArt?")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

// --- Missing error path tests ---

func TestGetAlbumNotFound(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getAlbum?id=nonexistent")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 70 {
		t.Errorf("error code = %v, want 70", apiErr["code"])
	}
}

func TestGetAlbumMissingID(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getAlbum?")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestGetSongNotFound(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getSong?id=nonexistent")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 70 {
		t.Errorf("error code = %v, want 70", apiErr["code"])
	}
}

func TestGetSongMissingID(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getSong?")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestGetSongsByGenreMissingGenre(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getSongsByGenre?")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestGetRandomSongsEmptyDB(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getRandomSongs?")

	rs := resp["randomSongs"].(map[string]any)
	songs := rs["song"].([]any)
	if len(songs) != 0 {
		t.Errorf("expected 0 songs on empty DB, got %d", len(songs))
	}
}

// --- Missing getAlbumList2 variant tests ---

func TestGetAlbumList2ByYear(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=byYear&fromYear=1990&toYear=1993")

	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 1 {
		t.Fatalf("expected 1 album (Gold 1992), got %d", len(albums))
	}
	a := albums[0].(map[string]any)
	if a["name"] != "Gold" {
		t.Errorf("album name = %v, want Gold", a["name"])
	}
}

func TestGetAlbumList2ByYearFullRange(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=byYear&fromYear=1990&toYear=2000")

	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 2 {
		t.Errorf("expected 2 albums, got %d", len(albums))
	}
}

func TestGetAlbumList2AlphabeticalByArtist(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=alphabeticalByArtist")

	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) < 2 {
		t.Fatalf("expected 2 albums, got %d", len(albums))
	}
	first := albums[0].(map[string]any)
	if first["artist"] != "ABBA" {
		t.Errorf("first artist = %v, want ABBA (alphabetical)", first["artist"])
	}
}

func TestGetAlbumList2Recent(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=recent")

	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 0 {
		t.Errorf("expected 0 albums for recent (stub), got %d", len(albums))
	}
}

func TestGetAlbumList2Frequent(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=frequent")

	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 0 {
		t.Errorf("expected 0 albums for frequent (stub), got %d", len(albums))
	}
}

func TestGetAlbumList2UnknownType(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=bogus")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status for unknown type")
	}
}

// --- Search album results ---

func TestSearch3AlbumResults(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/search3?query=Gold")

	sr := resp["searchResult3"].(map[string]any)
	albums := sr["album"].([]any)
	if len(albums) != 1 {
		t.Fatalf("expected 1 album for 'Gold', got %d", len(albums))
	}
	a := albums[0].(map[string]any)
	if a["name"] != "Gold" {
		t.Errorf("album name = %v, want Gold", a["name"])
	}
}

// --- Cover art resize tests ---

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

func TestGetCoverArtResize(t *testing.T) {
	srv := testServer(t)
	seedDataWithRealCover(t, srv)

	w := get(t, srv, "/rest/getCoverArt?id=alb1&size=50")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	// Verify the resized file was cached to disk.
	resizedPath := filepath.Join(srv.cfg.CoverArtDir, "alb1_50.jpg")
	if _, err := os.Stat(resizedPath); err != nil {
		t.Errorf("resized file not cached: %v", err)
	}
}

func TestGetCoverArtResizeCached(t *testing.T) {
	srv := testServer(t)
	seedDataWithRealCover(t, srv)

	// First request creates the cached file.
	w1 := get(t, srv, "/rest/getCoverArt?id=alb1&size=100")
	if w1.Code != http.StatusOK {
		t.Fatalf("first request: status = %d", w1.Code)
	}

	// Record mtime of the cached file — should not change on second request.
	resizedPath := filepath.Join(srv.cfg.CoverArtDir, "alb1_100.jpg")
	stat1, err := os.Stat(resizedPath)
	if err != nil {
		t.Fatalf("cached file not found after first request: %v", err)
	}

	// Second request should serve from cache without regenerating.
	w2 := get(t, srv, "/rest/getCoverArt?id=alb1&size=100")
	if w2.Code != http.StatusOK {
		t.Fatalf("second request: status = %d", w2.Code)
	}

	stat2, err := os.Stat(resizedPath)
	if err != nil {
		t.Fatalf("cached file disappeared: %v", err)
	}
	if !stat1.ModTime().Equal(stat2.ModTime()) {
		t.Errorf("cached file was regenerated: mtime changed from %v to %v", stat1.ModTime(), stat2.ModTime())
	}
}

func TestGetCoverArtNoSize(t *testing.T) {
	srv := testServer(t)
	seedDataWithRealCover(t, srv)

	// Without size param, should serve original.
	w := get(t, srv, "/rest/getCoverArt?id=alb1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	// No resized files should exist.
	matches, _ := filepath.Glob(filepath.Join(srv.cfg.CoverArtDir, "alb1_*.jpg"))
	if len(matches) != 0 {
		t.Errorf("unexpected resized files: %v", matches)
	}
}

// --- POST method routing ---

func TestPingPOST(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest("POST", "/rest/ping?f=json", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var wrapper subsonicJSON
	if err := json.Unmarshal(w.Body.Bytes(), &wrapper); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(wrapper.Response, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}
}

func TestPingViewSuffixPOST(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest("POST", "/rest/ping.view?f=json", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d for POST .view suffix", w.Code)
	}
}

// --- Auth middleware tests ---

const testEncryptionKey = "0123456789abcdef0123456789abcdef" // 32 hex chars = 16 bytes

// testServerWithAuth sets up a server with auth enabled and a seeded credential.
func testServerWithAuth(t *testing.T, username, password string) *Server {
	t.Helper()
	srv := testServer(t, testServerOpts{encryptionKey: testEncryptionKey})

	encrypted, err := auth.EncryptPassword(testEncryptionKey, password)
	if err != nil {
		t.Fatalf("EncryptPassword: %v", err)
	}

	conn, put, err := srv.db.WriteConn()
	if err != nil {
		t.Fatalf("WriteConn: %v", err)
	}
	err = sqlitex.ExecuteTransient(conn,
		`INSERT INTO credential (id, username, encrypted_password, client_name) VALUES (?, ?, ?, 'test')`,
		&sqlitex.ExecOptions{Args: []any{db.NewID(), username, encrypted}})
	put()
	if err != nil {
		t.Fatalf("seed credential: %v", err)
	}

	return srv
}

func TestAuthTokenSuccess(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")
	salt := "randomsalt"
	token := md5Hex("secret123" + salt)
	url := fmt.Sprintf("/rest/ping?f=json&u=alice&t=%s&s=%s", token, salt)

	resp := getJSON(t, srv, url)
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}
}

func TestAuthTokenWrongPassword(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")
	salt := "randomsalt"
	token := md5Hex("wrongpassword" + salt)
	url := fmt.Sprintf("/rest/ping?f=json&u=alice&t=%s&s=%s", token, salt)

	resp := getJSON(t, srv, url)
	if resp["status"] != "failed" {
		t.Errorf("expected failed status for wrong token")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 40 {
		t.Errorf("error code = %v, want 40", apiErr["code"])
	}
}

func TestAuthTokenWrongUsername(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")
	salt := "randomsalt"
	token := md5Hex("secret123" + salt)
	url := fmt.Sprintf("/rest/ping?f=json&u=bob&t=%s&s=%s", token, salt)

	resp := getJSON(t, srv, url)
	if resp["status"] != "failed" {
		t.Errorf("expected failed status for unknown user")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 40 {
		t.Errorf("error code = %v, want 40", apiErr["code"])
	}
}

func TestAuthLegacyPlaintext(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")
	url := "/rest/ping?f=json&u=alice&p=secret123"

	resp := getJSON(t, srv, url)
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}
}

func TestAuthLegacyHexEncoded(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")
	hexPass := hex.EncodeToString([]byte("secret123"))
	url := fmt.Sprintf("/rest/ping?f=json&u=alice&p=enc:%s", hexPass)

	resp := getJSON(t, srv, url)
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}
}

func TestAuthMissingUsername(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")
	url := "/rest/ping?f=json"

	resp := getJSON(t, srv, url)
	if resp["status"] != "failed" {
		t.Errorf("expected failed status for missing username")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestAuthMissingCredentials(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")
	url := "/rest/ping?f=json&u=alice"

	resp := getJSON(t, srv, url)
	if resp["status"] != "failed" {
		t.Errorf("expected failed status for missing credentials")
	}
}

func TestAuthDisabledFlag(t *testing.T) {
	// testServer sets AuthDisabled: true — requests should pass without credentials.
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/ping?")
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok (auth disabled)", resp["status"])
	}
}

func TestAuthExpiredCredential(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")

	// Update credential to be expired.
	conn, put, err := srv.db.WriteConn()
	if err != nil {
		t.Fatalf("WriteConn: %v", err)
	}
	err = sqlitex.ExecuteTransient(conn,
		`UPDATE credential SET expires_at = '2020-01-01T00:00:00' WHERE username = 'alice'`, nil)
	put()
	if err != nil {
		t.Fatalf("update credential: %v", err)
	}

	salt := "randomsalt"
	token := md5Hex("secret123" + salt)
	url := fmt.Sprintf("/rest/ping?f=json&u=alice&t=%s&s=%s", token, salt)

	resp := getJSON(t, srv, url)
	if resp["status"] != "failed" {
		t.Errorf("expected failed status for expired credential")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 40 {
		t.Errorf("error code = %v, want 40", apiErr["code"])
	}
}

func TestEncryptDecryptPasswordRoundTrip(t *testing.T) {
	password := "test-password-123"
	encrypted, err := auth.EncryptPassword(testEncryptionKey, password)
	if err != nil {
		t.Fatalf("EncryptPassword: %v", err)
	}

	decrypted, err := auth.DecryptPassword(testEncryptionKey, encrypted)
	if err != nil {
		t.Fatalf("DecryptPassword: %v", err)
	}

	if decrypted != password {
		t.Errorf("round-trip failed: got %q, want %q", decrypted, password)
	}
}

// --- Missing negative auth tests ---

func TestAuthLegacyPlaintextWrongPassword(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")

	resp := getJSON(t, srv, "/rest/ping?u=alice&p=wrongpassword")
	if resp["status"] != "failed" {
		t.Errorf("expected failed status for wrong plaintext password")
	}
	apiErr, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error in response")
	}
	if apiErr["code"].(float64) != 40 {
		t.Errorf("error code = %v, want 40", apiErr["code"])
	}
}

func TestAuthLegacyHexMalformed(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")

	resp := getJSON(t, srv, "/rest/ping?u=alice&p=enc:ZZZZ")
	if resp["status"] != "failed" {
		t.Errorf("expected failed status for malformed hex password")
	}
	apiErr, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error in response")
	}
	if apiErr["code"].(float64) != 40 {
		t.Errorf("error code = %v, want 40", apiErr["code"])
	}
}

// --- Missing negative cover art size tests ---

func TestGetCoverArtInvalidSize(t *testing.T) {
	srv := testServer(t)
	seedDataWithRealCover(t, srv)

	// Non-numeric size should serve the original without error.
	w := get(t, srv, "/rest/getCoverArt?id=alb1&size=abc")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for invalid size", w.Code)
	}

	// No resized files should exist.
	matches, _ := filepath.Glob(filepath.Join(srv.cfg.CoverArtDir, "alb1_*.jpg"))
	if len(matches) != 0 {
		t.Errorf("unexpected resized files for invalid size: %v", matches)
	}
}

func TestGetCoverArtSizeClamped(t *testing.T) {
	srv := testServer(t)
	seedDataWithRealCover(t, srv)

	// Size below minimum should be clamped to minCoverArtSize (32).
	w := get(t, srv, "/rest/getCoverArt?id=alb1&size=1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	// Should be clamped to min size, not size=1.
	clampedPath := filepath.Join(srv.cfg.CoverArtDir, "alb1_32.jpg")
	if _, err := os.Stat(clampedPath); err != nil {
		t.Errorf("expected clamped resize at min size: %v", err)
	}
	unclamped := filepath.Join(srv.cfg.CoverArtDir, "alb1_1.jpg")
	if _, err := os.Stat(unclamped); err == nil {
		t.Errorf("size=1 should have been clamped, but alb1_1.jpg exists")
	}
}
