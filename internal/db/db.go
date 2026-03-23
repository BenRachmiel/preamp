package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

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
		PoolSize: 4,
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

	// Set busy_timeout on all read connections so they retry instead of
	// failing immediately when the writer holds the WAL lock.
	for range 4 {
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

// NewID generates a random 16-byte hex ID.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
