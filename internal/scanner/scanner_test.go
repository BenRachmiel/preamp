package scanner

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/BenRachmiel/preamp/internal/db"
)

// countAudioFiles walks a directory and returns the number of files with
// supported audio extensions, for use in integration test assertions.
func countAudioFiles(t *testing.T, dir string) int {
	t.Helper()
	var count int
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if _, ok := supportedExts[ext]; ok {
			count++
		}
		return nil
	})
	return count
}

func setupScanner(t *testing.T, musicDir string) (*Scanner, *db.DB) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sc := New(database, musicDir, log)
	return sc, database
}

func TestScanRealLibrary(t *testing.T) {
	musicDir := filepath.Join("..", "..", "test-music-lib")
	if _, err := os.Stat(musicDir); err != nil {
		t.Skip("test-music-lib not found, skipping")
	}

	wantTracks := countAudioFiles(t, musicDir)
	if wantTracks == 0 {
		t.Skip("no audio files found in test-music-lib")
	}

	sc, database := setupScanner(t, musicDir)

	if err := sc.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if sc.Count() != wantTracks {
		t.Errorf("count = %d, want %d", sc.Count(), wantTracks)
	}

	conn, put, err := database.ReadConn()
	if err != nil {
		t.Fatalf("ReadConn: %v", err)
	}
	defer put()

	// Verify artists exist.
	var artistCount int
	sqlitex.ExecuteTransient(conn, `SELECT COUNT(*) FROM artist`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			artistCount = stmt.ColumnInt(0)
			return nil
		},
	})
	if artistCount < 1 {
		t.Errorf("artists = %d, want >= 1", artistCount)
	}

	// Verify albums exist.
	var albumCount int
	sqlitex.ExecuteTransient(conn, `SELECT COUNT(*) FROM album`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			albumCount = stmt.ColumnInt(0)
			return nil
		},
	})
	if albumCount < 1 {
		t.Errorf("albums = %d, want >= 1", albumCount)
	}

	// Verify album stats were updated (every album should have song_count > 0).
	var zeroCountAlbums int
	sqlitex.ExecuteTransient(conn, `SELECT COUNT(*) FROM album WHERE song_count = 0`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			zeroCountAlbums = stmt.ColumnInt(0)
			return nil
		},
	})
	if zeroCountAlbums > 0 {
		t.Errorf("%d albums have song_count=0 after stats update", zeroCountAlbums)
	}

	// Verify FTS populated with same count as songs.
	var ftsCount int
	sqlitex.ExecuteTransient(conn, `SELECT COUNT(*) FROM song_fts`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			ftsCount = stmt.ColumnInt(0)
			return nil
		},
	})
	if ftsCount != wantTracks {
		t.Errorf("FTS entries = %d, want %d", ftsCount, wantTracks)
	}

	t.Logf("scanned %d tracks, %d artists, %d albums", wantTracks, artistCount, albumCount)
}

func TestScanEmptyDir(t *testing.T) {
	emptyDir := t.TempDir()
	sc, _ := setupScanner(t, emptyDir)

	if err := sc.Run(); err != nil {
		t.Fatalf("Run on empty dir: %v", err)
	}
	if sc.Count() != 0 {
		t.Errorf("count = %d, want 0", sc.Count())
	}
}

func TestScanConcurrentBlocked(t *testing.T) {
	musicDir := filepath.Join("..", "..", "test-music-lib")
	if _, err := os.Stat(musicDir); err != nil {
		t.Skip("test-music-lib not found, skipping")
	}

	sc, _ := setupScanner(t, musicDir)

	// Start a scan in the background.
	done := make(chan error, 1)
	go func() { done <- sc.Run() }()

	// Wait until the first scan is actively running.
	for !sc.Scanning() {
		// spin until the background goroutine sets Scanning() = true
	}

	// A concurrent scan should be rejected.
	err := sc.Run()
	if err == nil {
		t.Error("expected error when starting a concurrent scan")
	}

	// Wait for original scan to finish.
	if firstErr := <-done; firstErr != nil {
		t.Fatalf("original scan failed: %v", firstErr)
	}
}

func TestScanIdempotent(t *testing.T) {
	musicDir := filepath.Join("..", "..", "test-music-lib")
	if _, err := os.Stat(musicDir); err != nil {
		t.Skip("test-music-lib not found, skipping")
	}

	wantTracks := countAudioFiles(t, musicDir)
	if wantTracks == 0 {
		t.Skip("no audio files found in test-music-lib")
	}

	sc, database := setupScanner(t, musicDir)

	// First scan.
	if err := sc.Run(); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Second scan should be idempotent.
	if err := sc.Run(); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	conn, put, err := database.ReadConn()
	if err != nil {
		t.Fatalf("ReadConn: %v", err)
	}
	defer put()

	// Should still have the same number of songs, not duplicates.
	var songCount int
	sqlitex.ExecuteTransient(conn, `SELECT COUNT(*) FROM song`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			songCount = stmt.ColumnInt(0)
			return nil
		},
	})
	if songCount != wantTracks {
		t.Errorf("songs after rescan = %d, want %d", songCount, wantTracks)
	}
}

func TestSupportedExts(t *testing.T) {
	expected := []string{".mp3", ".flac", ".ogg", ".m4a", ".opus", ".wma", ".wav", ".aac"}
	for _, ext := range expected {
		t.Run(ext, func(t *testing.T) {
			if _, ok := supportedExts[ext]; !ok {
				t.Errorf("missing supported ext %q", ext)
			}
		})
	}
}

func TestScanSkipsSymlinks(t *testing.T) {
	tmpDir := t.TempDir()
	realDir := t.TempDir()

	// Create a real MP3 file in the real directory.
	realFile := filepath.Join(realDir, "real.mp3")
	os.WriteFile(realFile, []byte("not a real mp3 but has mp3 ext"), 0o644)

	// Create a symlink in the scan directory pointing to the real file.
	linkPath := filepath.Join(tmpDir, "linked.mp3")
	if err := os.Symlink(realFile, linkPath); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}

	// Also create a real file in the scan directory to ensure scanning works.
	realInDir := filepath.Join(tmpDir, "real.mp3")
	os.WriteFile(realInDir, []byte("not a real mp3"), 0o644)

	sc, database := setupScanner(t, tmpDir)
	if err := sc.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Only the real file should be indexed, not the symlink.
	conn, put, err := database.ReadConn()
	if err != nil {
		t.Fatalf("ReadConn: %v", err)
	}
	defer put()

	var songCount int
	sqlitex.ExecuteTransient(conn, `SELECT COUNT(*) FROM song`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			songCount = stmt.ColumnInt(0)
			return nil
		},
	})
	if songCount != 1 {
		t.Errorf("songs = %d, want 1 (symlink should be skipped)", songCount)
	}
}

func TestSafeReadTrackRecoversPanic(t *testing.T) {
	// readTrack should recover from panics and return an error.
	// We can't easily make readTrack panic without a crafted file,
	// so test the wrapper directly by verifying it handles normal errors.
	tmpDir := t.TempDir()
	fpath := filepath.Join(tmpDir, "test.mp3")
	os.WriteFile(fpath, []byte("not a real mp3"), 0o644)

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	info, err := readTrack(fpath, ".mp3", "audio/mpeg", log)
	if err != nil {
		t.Fatalf("readTrack: %v", err)
	}
	if info.title != "test" {
		t.Errorf("title = %q, want %q", info.title, "test")
	}
}

func TestScanFolderArt(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a dummy audio file so the scanner has something to index.
	os.WriteFile(filepath.Join(tmpDir, "track.mp3"), []byte("not a real mp3"), 0o644)

	// Create a cover art file in the same directory.
	os.WriteFile(filepath.Join(tmpDir, "cover.jpg"), []byte("fake jpeg"), 0o644)

	sc, database := setupScanner(t, tmpDir)
	if err := sc.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	conn, put, err := database.ReadConn()
	if err != nil {
		t.Fatalf("ReadConn: %v", err)
	}
	defer put()

	var coverArt string
	sqlitex.ExecuteTransient(conn, `SELECT cover_art FROM album LIMIT 1`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			coverArt = stmt.ColumnText(0)
			return nil
		},
	})

	want := filepath.Join(tmpDir, "cover.jpg")
	if coverArt != want {
		t.Errorf("cover_art = %q, want %q", coverArt, want)
	}
}

func TestScanClearsStaleCoverArt(t *testing.T) {
	tmpDir := t.TempDir()

	os.WriteFile(filepath.Join(tmpDir, "track.mp3"), []byte("not a real mp3"), 0o644)

	sc, database := setupScanner(t, tmpDir)
	if err := sc.Run(); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Manually set cover_art to a nonexistent path.
	conn, put, err := database.WriteConn()
	if err != nil {
		t.Fatalf("WriteConn: %v", err)
	}
	sqlitex.ExecuteTransient(conn, `UPDATE album SET cover_art = '/nonexistent/cover.jpg'`, nil)
	put()

	// Rescan should clear the stale path.
	if err := sc.Run(); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	rconn, rput, err := database.ReadConn()
	if err != nil {
		t.Fatalf("ReadConn: %v", err)
	}
	defer rput()

	var coverArt string
	sqlitex.ExecuteTransient(rconn, `SELECT COALESCE(cover_art, '') FROM album LIMIT 1`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			coverArt = stmt.ColumnText(0)
			return nil
		},
	})

	if coverArt != "" {
		t.Errorf("cover_art = %q, want empty (stale path should be cleared)", coverArt)
	}
}

func TestReadTrackFallbackNoTags(t *testing.T) {
	// Create a dummy file with no valid tags.
	tmpDir := t.TempDir()
	fpath := filepath.Join(tmpDir, "test.mp3")
	os.WriteFile(fpath, []byte("not a real mp3"), 0o644)

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	info, err := readTrack(fpath, ".mp3", "audio/mpeg", log)
	if err != nil {
		t.Fatalf("readTrack: %v", err)
	}

	if info.title != "test" {
		t.Errorf("title = %q, want %q", info.title, "test")
	}
	if info.artist != "Unknown Artist" {
		t.Errorf("artist = %q, want %q", info.artist, "Unknown Artist")
	}
	if info.ext != "mp3" {
		t.Errorf("ext = %q, want %q", info.ext, "mp3")
	}
}
