package api

import (
	"net/http"

	"github.com/BenRachmiel/preamp/internal/db"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

const (
	itemTypeSong   = "song"
	itemTypeAlbum  = "album"
	itemTypeArtist = "artist"
)

// starItems maps param names to their item_type for star/unstar operations.
var starParams = []struct {
	param    string
	itemType string
}{
	{"id", itemTypeSong},
	{"albumId", itemTypeAlbum},
	{"artistId", itemTypeArtist},
}

func (s *Server) handleStar(w http.ResponseWriter, r *http.Request) {
	username, have := requireUsername(w, r)
	if !have {
		return
	}

	if err := r.ParseForm(); err != nil {
		writeError(w, r, 0, "invalid request")
		return
	}

	var hasIDs bool
	for _, sp := range starParams {
		if len(r.Form[sp.param]) > 0 {
			hasIDs = true
			break
		}
	}
	if !hasIDs {
		writeError(w, r, 10, "missing parameter: id, albumId, or artistId")
		return
	}

	conn, put, err := s.db.WriteConn()
	if err != nil {
		writeError(w, r, 0, "database error")
		return
	}
	defer put()

	if err := starInsert(conn, username, r, starParams); err != nil {
		writeError(w, r, 0, "database error")
		return
	}

	writeOK(w, r)
}

func (s *Server) handleUnstar(w http.ResponseWriter, r *http.Request) {
	username, have := requireUsername(w, r)
	if !have {
		return
	}

	if err := r.ParseForm(); err != nil {
		writeError(w, r, 0, "invalid request")
		return
	}

	var hasIDs bool
	for _, sp := range starParams {
		if len(r.Form[sp.param]) > 0 {
			hasIDs = true
			break
		}
	}
	if !hasIDs {
		writeError(w, r, 10, "missing parameter: id, albumId, or artistId")
		return
	}

	conn, put, err := s.db.WriteConn()
	if err != nil {
		writeError(w, r, 0, "database error")
		return
	}
	defer put()

	if err := starDelete(conn, username, r, starParams); err != nil {
		writeError(w, r, 0, "database error")
		return
	}

	writeOK(w, r)
}

// starInsert inserts stars for all provided IDs within a single transaction.
func starInsert(conn *sqlite.Conn, username string, r *http.Request, params []struct {
	param    string
	itemType string
}) error {
	if err := sqlitex.ExecuteTransient(conn, "BEGIN", nil); err != nil {
		return err
	}
	for _, sp := range params {
		for _, id := range r.Form[sp.param] {
			err := sqlitex.ExecuteTransient(conn,
				`INSERT OR IGNORE INTO star (id, user_id, item_id, item_type) VALUES (?, ?, ?, ?)`,
				&sqlitex.ExecOptions{Args: []any{db.NewID(), username, id, sp.itemType}})
			if err != nil {
				sqlitex.ExecuteTransient(conn, "ROLLBACK", nil)
				return err
			}
		}
	}
	return sqlitex.ExecuteTransient(conn, "COMMIT", nil)
}

// starDelete removes stars for all provided IDs within a single transaction.
func starDelete(conn *sqlite.Conn, username string, r *http.Request, params []struct {
	param    string
	itemType string
}) error {
	if err := sqlitex.ExecuteTransient(conn, "BEGIN", nil); err != nil {
		return err
	}
	for _, sp := range params {
		for _, id := range r.Form[sp.param] {
			err := sqlitex.ExecuteTransient(conn,
				`DELETE FROM star WHERE user_id = ? AND item_id = ? AND item_type = ?`,
				&sqlitex.ExecOptions{Args: []any{username, id, sp.itemType}})
			if err != nil {
				sqlitex.ExecuteTransient(conn, "ROLLBACK", nil)
				return err
			}
		}
	}
	return sqlitex.ExecuteTransient(conn, "COMMIT", nil)
}
