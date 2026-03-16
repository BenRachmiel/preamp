package api

import (
	"log/slog"
	"net/http"
	"strings"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func (s *Server) handleSetRating(w http.ResponseWriter, r *http.Request) {
	username, have := requireUsername(w, r)
	if !have {
		return
	}

	id := r.FormValue("id")
	if id == "" {
		writeError(w, r, 10, "missing parameter: id")
		return
	}

	rating := intParam(r, "rating", -1)
	if rating < 0 || rating > 5 {
		writeError(w, r, 10, "rating must be between 0 and 5")
		return
	}

	conn, put, err := s.db.WriteConn()
	if err != nil {
		writeError(w, r, 0, "database error")
		return
	}
	defer put()

	if rating == 0 {
		err = sqlitex.ExecuteTransient(conn,
			`DELETE FROM rating WHERE user_id = ? AND item_id = ?`,
			&sqlitex.ExecOptions{Args: []any{username, id}})
	} else {
		err = sqlitex.ExecuteTransient(conn,
			`INSERT INTO rating (user_id, item_id, rating) VALUES (?, ?, ?)
			 ON CONFLICT(user_id, item_id) DO UPDATE SET rating = excluded.rating`,
			&sqlitex.ExecOptions{Args: []any{username, id, rating}})
	}
	if err != nil {
		writeError(w, r, 0, "database error")
		return
	}

	writeOK(w, r)
}

// decorateRatings stamps UserRating onto songs for the given user.
func decorateRatings(conn *sqlite.Conn, username string, songs []SongID3) {
	if username == "" || len(songs) == 0 {
		return
	}

	placeholders := strings.Repeat(",?", len(songs))[1:]
	args := make([]any, 0, 1+len(songs))
	args = append(args, username)
	for i := range songs {
		args = append(args, songs[i].ID)
	}

	ratings := make(map[string]int, len(songs))
	if err := sqlitex.ExecuteTransient(conn,
		`SELECT item_id, rating FROM rating WHERE user_id = ? AND item_id IN (`+placeholders+`)`,
		&sqlitex.ExecOptions{
			Args: args,
			ResultFunc: func(stmt *sqlite.Stmt) error {
				ratings[stmt.ColumnText(0)] = stmt.ColumnInt(1)
				return nil
			},
		}); err != nil {
		slog.Warn("decorateRatings query failed", "err", err)
		return
	}

	for i := range songs {
		if r, ok := ratings[songs[i].ID]; ok {
			songs[i].UserRating = r
		}
	}
}
