package db

import (
	"testing"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func TestOpen(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	conn, put, err := database.WriteConn()
	if err != nil {
		t.Fatalf("WriteConn: %v", err)
	}
	defer put()

	err = sqlitex.ExecuteTransient(conn, `INSERT INTO artist (id, name) VALUES ('a1', 'Test Artist')`, nil)
	if err != nil {
		t.Fatalf("insert artist: %v", err)
	}

	rconn, rput, err := database.ReadConn()
	if err != nil {
		t.Fatalf("ReadConn: %v", err)
	}
	defer rput()

	var name string
	err = sqlitex.ExecuteTransient(rconn, `SELECT name FROM artist WHERE id = 'a1'`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			name = stmt.ColumnText(0)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if name != "Test Artist" {
		t.Errorf("got name %q, want %q", name, "Test Artist")
	}
}

func TestSeparatePools(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	// Hold the single write connection.
	wconn, wput, err := database.WriteConn()
	if err != nil {
		t.Fatalf("WriteConn: %v", err)
	}
	_ = wconn

	// Read pool should still be available.
	rconn, rput, err := database.ReadConn()
	if err != nil {
		t.Fatalf("ReadConn while write held: %v", err)
	}
	_ = rconn
	rput()
	wput()
}

func TestWALMode(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	conn, put, err := database.ReadConn()
	if err != nil {
		t.Fatalf("ReadConn: %v", err)
	}
	defer put()

	var mode string
	err = sqlitex.ExecuteTransient(conn, `PRAGMA journal_mode`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			mode = stmt.ColumnText(0)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want %q", mode, "wal")
	}
}

func TestNewID(t *testing.T) {
	id1 := NewID()
	id2 := NewID()

	if len(id1) != 32 {
		t.Errorf("NewID length = %d, want 32", len(id1))
	}
	if id1 == id2 {
		t.Error("NewID generated duplicate IDs")
	}
}

func TestReadConnBusyTimeout(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	conn, put, err := database.ReadConn()
	if err != nil {
		t.Fatalf("ReadConn: %v", err)
	}
	defer put()

	var timeout int
	err = sqlitex.ExecuteTransient(conn, `PRAGMA busy_timeout`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			timeout = stmt.ColumnInt(0)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if timeout != 5000 {
		t.Errorf("read conn busy_timeout = %d, want 5000", timeout)
	}
}

func TestUniqueArtistName(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	conn, put, err := database.WriteConn()
	if err != nil {
		t.Fatalf("WriteConn: %v", err)
	}
	defer put()

	err = sqlitex.ExecuteTransient(conn, `INSERT INTO artist (id, name) VALUES ('a1', 'Duped')`, nil)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	err = sqlitex.ExecuteTransient(conn, `INSERT INTO artist (id, name) VALUES ('a2', 'Duped')`, nil)
	if err == nil {
		t.Error("expected UNIQUE constraint error for duplicate artist name")
	}
}

func TestUniqueSongPath(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	conn, put, err := database.WriteConn()
	if err != nil {
		t.Fatalf("WriteConn: %v", err)
	}
	defer put()

	// Set up artist and album.
	sqlitex.ExecuteTransient(conn, `INSERT INTO artist (id, name) VALUES ('a1', 'A')`, nil)
	sqlitex.ExecuteTransient(conn, `INSERT INTO album (id, artist_id, name) VALUES ('al1', 'a1', 'B')`, nil)

	insert := `INSERT INTO song (id, album_id, artist_id, title, track, duration, size, suffix, bitrate, content_type, path)
		VALUES (?, 'al1', 'a1', 'T', 1, 100, 1000, 'mp3', 320, 'audio/mpeg', '/same/path.mp3')`

	err = sqlitex.ExecuteTransient(conn, insert, &sqlitex.ExecOptions{Args: []any{"s1"}})
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	err = sqlitex.ExecuteTransient(conn, insert, &sqlitex.ExecOptions{Args: []any{"s2"}})
	if err == nil {
		t.Error("expected UNIQUE constraint error for duplicate song path")
	}
}

func TestForeignKeyAlbumArtist(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	conn, put, err := database.WriteConn()
	if err != nil {
		t.Fatalf("WriteConn: %v", err)
	}
	defer put()

	// Enable FK enforcement (SQLite has it off by default).
	sqlitex.ExecuteTransient(conn, `PRAGMA foreign_keys = ON`, nil)

	err = sqlitex.ExecuteTransient(conn, `INSERT INTO album (id, artist_id, name) VALUES ('al1', 'nonexistent', 'X')`, nil)
	if err == nil {
		t.Error("expected foreign key error for nonexistent artist_id")
	}
}

func TestSchemaAllTables(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	conn, put, err := database.ReadConn()
	if err != nil {
		t.Fatalf("ReadConn: %v", err)
	}
	defer put()

	tables := map[string]bool{}
	err = sqlitex.ExecuteTransient(conn, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			tables[stmt.ColumnText(0)] = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("listing tables: %v", err)
	}

	expected := []string{"artist", "album", "song", "credential", "star", "play_history", "song_fts"}
	for _, want := range expected {
		t.Run(want, func(t *testing.T) {
			if !tables[want] {
				t.Errorf("missing table %q", want)
			}
		})
	}
}
