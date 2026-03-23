package db

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"strings"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

const readPoolSize = 4

// DB holds separate write and read connection pools per design constraints.
type DB struct {
	write *sqlitex.Pool
	read  *sqlitex.Pool
}

func Open(path string) (*DB, error) {
	write, err := sqlitex.NewPool(path, sqlitex.PoolOptions{
		PoolSize: 1,
		Flags:    sqlite.OpenReadWrite | sqlite.OpenCreate | sqlite.OpenWAL,
	})
	if err != nil {
		return nil, fmt.Errorf("opening write pool: %w", err)
	}

	read, err := sqlitex.NewPool(path, sqlitex.PoolOptions{
		PoolSize: readPoolSize,
		Flags:    sqlite.OpenReadOnly | sqlite.OpenWAL,
	})
	if err != nil {
		write.Close()
		return nil, fmt.Errorf("opening read pool: %w", err)
	}

	db := &DB{write: write, read: read}
	if err := db.init(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) init() error {
	conn, err := db.write.Take(context.Background())
	if err != nil {
		return fmt.Errorf("taking write conn: %w", err)
	}
	defer db.write.Put(conn)

	for _, pragma := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA journal_mode = WAL",
		"PRAGMA cache_size = -64000",
		"PRAGMA mmap_size = 30000000",
		"PRAGMA temp_store = MEMORY",
	} {
		if err := sqlitex.ExecuteTransient(conn, pragma, nil); err != nil {
			return fmt.Errorf("pragma %q: %w", pragma, err)
		}
	}

	if err := sqlitex.ExecuteScript(conn, schema, nil); err != nil {
		return err
	}

	// Idempotent column migration — SQLite has no ADD COLUMN IF NOT EXISTS.
	// Only ignore "duplicate column name" errors; propagate anything else.
	if err := sqlitex.ExecuteTransient(conn,
		`ALTER TABLE song ADD COLUMN file_mtime INTEGER NOT NULL DEFAULT 0`, nil); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("migration file_mtime: %w", err)
		}
	}

	// Set busy_timeout on all read connections so they retry instead of
	// failing immediately when the writer holds the WAL lock.
	for range readPoolSize {
		rc, err := db.read.Take(context.Background())
		if err != nil {
			return fmt.Errorf("taking read conn for pragma: %w", err)
		}
		if err := sqlitex.ExecuteTransient(rc, "PRAGMA busy_timeout = 5000", nil); err != nil {
			db.read.Put(rc)
			return fmt.Errorf("read pragma busy_timeout: %w", err)
		}
		db.read.Put(rc)
	}

	return nil
}

// WriteConn borrows the single write connection. Caller must call put when done.
func (db *DB) WriteConn() (conn *sqlite.Conn, put func(), err error) {
	c, err := db.write.Take(context.Background())
	if err != nil {
		return nil, nil, err
	}
	return c, func() { db.write.Put(c) }, nil
}

// ReadConn borrows a read connection. Caller must call put when done.
func (db *DB) ReadConn() (conn *sqlite.Conn, put func(), err error) {
	c, err := db.read.Take(context.Background())
	if err != nil {
		return nil, nil, err
	}
	return c, func() { db.read.Put(c) }, nil
}

func (db *DB) Close() {
	if db.write != nil {
		db.write.Close()
	}
	if db.read != nil {
		db.read.Close()
	}
}

// NewID generates a random 16-byte hex ID using a fast PRNG.
// These IDs are internal primary keys, not security tokens — crypto/rand
// is unnecessary and its per-call syscall overhead is significant during scans.
func NewID() string {
	var b [16]byte
	binary.LittleEndian.PutUint64(b[:8], rand.Uint64())
	binary.LittleEndian.PutUint64(b[8:], rand.Uint64())
	return hex.EncodeToString(b[:])
}
