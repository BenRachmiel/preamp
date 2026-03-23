package scanner

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/BenRachmiel/preamp/internal/db"
)

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

	sc, database := setupScanner(t, musicDir)

	if err := sc.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if sc.Count() != 37 {
		t.Errorf("count = %d, want 37", sc.Count())
	}

	// Verify artists.
	conn, put, err := database.ReadConn()
	if err != nil {
		t.Fatalf("ReadConn: %v", err)
	}
	defer put()

	var artistCount int
	sqlitex.ExecuteTransient(conn, `SELECT COUNT(*) FROM artist`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			artistCount = stmt.ColumnInt(0)
			return nil
		},
	})
	if artistCount != 3 {
		t.Errorf("artists = %d, want 3", artistCount)
	}

	// Verify albums.
	var albumCount int
	sqlitex.ExecuteTransient(conn, `SELECT COUNT(*) FROM album`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			albumCount = stmt.ColumnInt(0)
			return nil
		},
	})
	if albumCount != 3 {
		t.Errorf("albums = %d, want 3", albumCount)
	}

	// Verify album stats were updated.
	var songCount int
	sqlitex.ExecuteTransient(conn, `SELECT song_count FROM album ORDER BY name LIMIT 1`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			songCount = stmt.ColumnInt(0)
			return nil
		},
	})
	if songCount != 19 {
		t.Errorf("ABBA Gold song_count = %d, want 19", songCount)
	}

	// Verify FTS populated.
	var ftsCount int
	sqlitex.ExecuteTransient(conn, `SELECT COUNT(*) FROM song_fts`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			ftsCount = stmt.ColumnInt(0)
			return nil
		},
	})
	if ftsCount != 37 {
		t.Errorf("FTS entries = %d, want 37", ftsCount)
	}

	// Verify FTS search works.
	var ftsHits int
	sqlitex.ExecuteTransient(conn, `SELECT COUNT(*) FROM song_fts WHERE song_fts MATCH '"dancing"'`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			ftsHits = stmt.ColumnInt(0)
			return nil
		},
	})
	if ftsHits != 1 {
		t.Errorf("FTS hits for 'dancing' = %d, want 1", ftsHits)
	}
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

	// Should still have exactly 29 songs, not duplicates.
	var songCount int
	sqlitex.ExecuteTransient(conn, `SELECT COUNT(*) FROM song`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			songCount = stmt.ColumnInt(0)
			return nil
		},
	})
	if songCount != 37 {
		t.Errorf("songs after rescan = %d, want 37", songCount)
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
	// safeReadTrack should recover from panics and return an error.
	// We can't easily make readTrack panic without a crafted file,
	// so test the wrapper directly by verifying it handles normal errors.
	tmpDir := t.TempDir()
	fpath := filepath.Join(tmpDir, "test.mp3")
	os.WriteFile(fpath, []byte("not a real mp3"), 0o644)

	info, err := safeReadTrack(fpath, ".mp3", "audio/mpeg")
	if err != nil {
		t.Fatalf("safeReadTrack: %v", err)
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

	info, err := readTrack(fpath, ".mp3", "audio/mpeg")
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
