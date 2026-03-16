package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/BenRachmiel/preamp/internal/db"
	"zombiezen.com/go/sqlite/sqlitex"
)

func (s *Server) handleScrobble(w http.ResponseWriter, r *http.Request) {
	username, have := requireUsername(w, r)
	if !have {
		return
	}

	if err := r.ParseForm(); err != nil {
		writeError(w, r, 0, "invalid request")
		return
	}

	ids := r.Form["id"]
	if len(ids) == 0 {
		writeError(w, r, 10, "missing parameter: id")
		return
	}

	// Parse optional time params (epoch ms, one per id).
	times := r.Form["time"]

	conn, put, err := s.db.WriteConn()
	if err != nil {
		writeError(w, r, 0, "database error")
		return
	}
	defer put()

	if err := sqlitex.ExecuteTransient(conn, "BEGIN", nil); err != nil {
		writeError(w, r, 0, "database error")
		return
	}

	for i, songID := range ids {
		playedAt := time.Now().UTC().Format("2006-01-02T15:04:05")
		if i < len(times) {
			if ms, err := strconv.ParseInt(times[i], 10, 64); err == nil {
				playedAt = time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05")
			}
		}
		err := sqlitex.ExecuteTransient(conn,
			`INSERT INTO play_history (id, song_id, user_id, played_at) VALUES (?, ?, ?, ?)`,
			&sqlitex.ExecOptions{Args: []any{db.NewID(), songID, username, playedAt}})
		if err != nil {
			sqlitex.ExecuteTransient(conn, "ROLLBACK", nil)
			writeError(w, r, 0, "database error")
			return
		}
	}

	if err := sqlitex.ExecuteTransient(conn, "COMMIT", nil); err != nil {
		writeError(w, r, 0, "database error")
		return
	}

	writeOK(w, r)
}
