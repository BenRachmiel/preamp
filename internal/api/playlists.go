package api

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/BenRachmiel/preamp/internal/db"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func (s *Server) handleGetPlaylists(w http.ResponseWriter, r *http.Request) {
	username, have := requireUsername(w, r)
	if !have {
		return
	}

	conn, put, err := s.db.ReadConn()
	if err != nil {
		writeError(w, r, 0, "database error")
		return
	}
	defer put()

	playlists := []PlaylistEntry{}
	err = sqlitex.ExecuteTransient(conn, `
		SELECT p.id, p.name, p.comment, p.user_id, p.public, p.created_at, p.updated_at,
		       COALESCE(SUM(s.duration), 0), COUNT(ps.song_id)
		FROM playlist p
		LEFT JOIN playlist_song ps ON ps.playlist_id = p.id
		LEFT JOIN song s ON s.id = ps.song_id
		WHERE p.user_id = ?
		GROUP BY p.id
		ORDER BY p.name COLLATE NOCASE
	`, &sqlitex.ExecOptions{
		Args: []any{username},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			playlists = append(playlists, PlaylistEntry{
				ID:        stmt.ColumnText(0),
				Name:      stmt.ColumnText(1),
				Comment:   stmt.ColumnText(2),
				Owner:     stmt.ColumnText(3),
				Public:    stmt.ColumnInt(4) != 0,
				Created:   stmt.ColumnText(5),
				Changed:   stmt.ColumnText(6),
				Duration:  stmt.ColumnInt(7),
				SongCount: stmt.ColumnInt(8),
			})
			return nil
		},
	})
	if err != nil {
		writeError(w, r, 0, "query error")
		return
	}

	resp := ok()
	resp.Playlists = &Playlists{Playlists: playlists}
	writeResponse(w, r, resp)
}

func (s *Server) handleGetPlaylist(w http.ResponseWriter, r *http.Request) {
	_, have := requireUsername(w, r)
	if !have {
		return
	}

	id := r.FormValue("id")
	if id == "" {
		writeError(w, r, 10, "missing parameter: id")
		return
	}

	conn, put, err := s.db.ReadConn()
	if err != nil {
		writeError(w, r, 0, "database error")
		return
	}
	defer put()

	var pl PlaylistWithSongs
	found := false

	err = sqlitex.ExecuteTransient(conn, `
		SELECT p.id, p.name, p.comment, p.user_id, p.public, p.created_at, p.updated_at
		FROM playlist p WHERE p.id = ?
	`, &sqlitex.ExecOptions{
		Args: []any{id},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			pl.ID = stmt.ColumnText(0)
			pl.Name = stmt.ColumnText(1)
			pl.Comment = stmt.ColumnText(2)
			pl.Owner = stmt.ColumnText(3)
			pl.Public = stmt.ColumnInt(4) != 0
			pl.Created = stmt.ColumnText(5)
			pl.Changed = stmt.ColumnText(6)
			found = true
			return nil
		},
	})
	if err != nil || !found {
		writeError(w, r, 70, "playlist not found")
		return
	}

	pl.Songs = []SongID3{}
	err = sqlitex.ExecuteTransient(conn, `
		SELECT s.id, s.title, s.track, s.disc, s.year, s.genre,
		       s.duration, s.size, s.suffix, s.bitrate, s.content_type, s.path,
		       a.name, a.id, al.name, al.id, al.cover_art
		FROM playlist_song ps
		JOIN song s ON s.id = ps.song_id
		JOIN artist a ON a.id = s.artist_id
		JOIN album al ON al.id = s.album_id
		WHERE ps.playlist_id = ?
		ORDER BY ps.position
	`, &sqlitex.ExecOptions{
		Args: []any{id},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			pl.Songs = append(pl.Songs, songFromRow(stmt))
			return nil
		},
	})
	if err != nil {
		writeError(w, r, 0, "query error")
		return
	}

	pl.SongCount = len(pl.Songs)
	totalDuration := 0
	for _, song := range pl.Songs {
		totalDuration += song.Duration
	}
	pl.Duration = totalDuration

	username := usernameFromRequest(r)
	decorateRatings(conn, username, pl.Songs)

	resp := ok()
	resp.Playlist = &pl
	writeResponse(w, r, resp)
}

func (s *Server) handleCreatePlaylist(w http.ResponseWriter, r *http.Request) {
	username, have := requireUsername(w, r)
	if !have {
		return
	}

	if err := r.ParseForm(); err != nil {
		writeError(w, r, 0, "invalid request")
		return
	}

	playlistID := r.FormValue("playlistId")
	name := r.FormValue("name")
	songIDs := r.Form["songId"]

	const maxPlaylistSongs = 10_000
	if len(songIDs) > maxPlaylistSongs {
		writeError(w, r, 0, fmt.Sprintf("too many songs: %d (max %d)", len(songIDs), maxPlaylistSongs))
		return
	}

	// Subsonic spec: playlistId = update existing, otherwise name is required.
	if playlistID == "" && name == "" {
		writeError(w, r, 10, "missing parameter: name or playlistId")
		return
	}

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

	if playlistID != "" {
		// Update existing: clear songs, optionally update name.
		if name != "" {
			err = sqlitex.ExecuteTransient(conn,
				`UPDATE playlist SET name = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%S', 'now') WHERE id = ? AND user_id = ?`,
				&sqlitex.ExecOptions{Args: []any{name, playlistID, username}})
			if err != nil {
				sqlitex.ExecuteTransient(conn, "ROLLBACK", nil)
				writeError(w, r, 0, "database error")
				return
			}
		}
		err = sqlitex.ExecuteTransient(conn,
			`DELETE FROM playlist_song WHERE playlist_id = ?`,
			&sqlitex.ExecOptions{Args: []any{playlistID}})
		if err != nil {
			sqlitex.ExecuteTransient(conn, "ROLLBACK", nil)
			writeError(w, r, 0, "database error")
			return
		}
	} else {
		playlistID = db.NewID()
		err = sqlitex.ExecuteTransient(conn,
			`INSERT INTO playlist (id, user_id, name) VALUES (?, ?, ?)`,
			&sqlitex.ExecOptions{Args: []any{playlistID, username, name}})
		if err != nil {
			sqlitex.ExecuteTransient(conn, "ROLLBACK", nil)
			writeError(w, r, 0, "database error")
			return
		}
	}

	for i, songID := range songIDs {
		err = sqlitex.ExecuteTransient(conn,
			`INSERT INTO playlist_song (playlist_id, song_id, position) VALUES (?, ?, ?)`,
			&sqlitex.ExecOptions{Args: []any{playlistID, songID, i}})
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

	// Read back the created/updated playlist using the write conn (it can read too).
	pl := PlaylistWithSongs{Songs: []SongID3{}}
	err = sqlitex.ExecuteTransient(conn, `
		SELECT p.id, p.name, p.comment, p.user_id, p.public, p.created_at, p.updated_at
		FROM playlist p WHERE p.id = ?
	`, &sqlitex.ExecOptions{
		Args: []any{playlistID},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			pl.ID = stmt.ColumnText(0)
			pl.Name = stmt.ColumnText(1)
			pl.Comment = stmt.ColumnText(2)
			pl.Owner = stmt.ColumnText(3)
			pl.Public = stmt.ColumnInt(4) != 0
			pl.Created = stmt.ColumnText(5)
			pl.Changed = stmt.ColumnText(6)
			return nil
		},
	})
	if err != nil {
		// Playlist was already committed — read-back is best-effort.
		writeOK(w, r)
		return
	}

	err = sqlitex.ExecuteTransient(conn, `
		SELECT s.id, s.title, s.track, s.disc, s.year, s.genre,
		       s.duration, s.size, s.suffix, s.bitrate, s.content_type, s.path,
		       a.name, a.id, al.name, al.id, al.cover_art
		FROM playlist_song ps
		JOIN song s ON s.id = ps.song_id
		JOIN artist a ON a.id = s.artist_id
		JOIN album al ON al.id = s.album_id
		WHERE ps.playlist_id = ?
		ORDER BY ps.position
	`, &sqlitex.ExecOptions{
		Args: []any{playlistID},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			pl.Songs = append(pl.Songs, songFromRow(stmt))
			return nil
		},
	})
	if err != nil {
		// Playlist was already committed — read-back is best-effort.
		writeOK(w, r)
		return
	}

	pl.SongCount = len(pl.Songs)
	totalDuration := 0
	for _, song := range pl.Songs {
		totalDuration += song.Duration
	}
	pl.Duration = totalDuration

	decorateRatings(conn, username, pl.Songs)

	resp := ok()
	resp.Playlist = &pl
	writeResponse(w, r, resp)
}

func (s *Server) handleUpdatePlaylist(w http.ResponseWriter, r *http.Request) {
	username, have := requireUsername(w, r)
	if !have {
		return
	}

	if err := r.ParseForm(); err != nil {
		writeError(w, r, 0, "invalid request")
		return
	}

	playlistID := r.FormValue("playlistId")
	if playlistID == "" {
		writeError(w, r, 10, "missing parameter: playlistId")
		return
	}

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

	// Update metadata fields if provided.
	if name := r.FormValue("name"); name != "" {
		err = sqlitex.ExecuteTransient(conn,
			`UPDATE playlist SET name = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%S', 'now') WHERE id = ? AND user_id = ?`,
			&sqlitex.ExecOptions{Args: []any{name, playlistID, username}})
		if err != nil {
			sqlitex.ExecuteTransient(conn, "ROLLBACK", nil)
			writeError(w, r, 0, "database error")
			return
		}
	}
	if comment, ok := r.Form["comment"]; ok {
		err = sqlitex.ExecuteTransient(conn,
			`UPDATE playlist SET comment = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%S', 'now') WHERE id = ? AND user_id = ?`,
			&sqlitex.ExecOptions{Args: []any{comment[0], playlistID, username}})
		if err != nil {
			sqlitex.ExecuteTransient(conn, "ROLLBACK", nil)
			writeError(w, r, 0, "database error")
			return
		}
	}
	if pub, ok := r.Form["public"]; ok {
		pubVal := 0
		if pub[0] == "true" {
			pubVal = 1
		}
		err = sqlitex.ExecuteTransient(conn,
			`UPDATE playlist SET public = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%S', 'now') WHERE id = ? AND user_id = ?`,
			&sqlitex.ExecOptions{Args: []any{pubVal, playlistID, username}})
		if err != nil {
			sqlitex.ExecuteTransient(conn, "ROLLBACK", nil)
			writeError(w, r, 0, "database error")
			return
		}
	}

	// Remove songs by index (must process before adds, highest index first).
	removeIndices := r.Form["songIndexToRemove"]
	if len(removeIndices) > 0 {
		indices := make([]int, 0, len(removeIndices))
		for _, s := range removeIndices {
			if idx, err := strconv.Atoi(s); err == nil {
				indices = append(indices, idx)
			}
		}
		sort.Sort(sort.Reverse(sort.IntSlice(indices)))
		for _, idx := range indices {
			err = sqlitex.ExecuteTransient(conn,
				`DELETE FROM playlist_song WHERE playlist_id = ? AND position = ?`,
				&sqlitex.ExecOptions{Args: []any{playlistID, idx}})
			if err != nil {
				sqlitex.ExecuteTransient(conn, "ROLLBACK", nil)
				writeError(w, r, 0, "database error")
				return
			}
		}
		// Resequence positions.
		if err := resequencePlaylist(conn, playlistID); err != nil {
			sqlitex.ExecuteTransient(conn, "ROLLBACK", nil)
			writeError(w, r, 0, "database error")
			return
		}
	}

	// Add songs.
	addIDs := r.Form["songIdToAdd"]
	if len(addIDs) > 0 {
		// Find current max position.
		maxPos := -1
		err = sqlitex.ExecuteTransient(conn,
			`SELECT COALESCE(MAX(position), -1) FROM playlist_song WHERE playlist_id = ?`,
			&sqlitex.ExecOptions{
				Args: []any{playlistID},
				ResultFunc: func(stmt *sqlite.Stmt) error {
					maxPos = stmt.ColumnInt(0)
					return nil
				},
			})
		if err != nil {
			sqlitex.ExecuteTransient(conn, "ROLLBACK", nil)
			writeError(w, r, 0, "database error")
			return
		}
		for i, songID := range addIDs {
			err = sqlitex.ExecuteTransient(conn,
				`INSERT INTO playlist_song (playlist_id, song_id, position) VALUES (?, ?, ?)`,
				&sqlitex.ExecOptions{Args: []any{playlistID, songID, maxPos + 1 + i}})
			if err != nil {
				sqlitex.ExecuteTransient(conn, "ROLLBACK", nil)
				writeError(w, r, 0, "database error")
				return
			}
		}
	}

	if err := sqlitex.ExecuteTransient(conn, "COMMIT", nil); err != nil {
		writeError(w, r, 0, "database error")
		return
	}

	writeOK(w, r)
}

func (s *Server) handleDeletePlaylist(w http.ResponseWriter, r *http.Request) {
	username, have := requireUsername(w, r)
	if !have {
		return
	}

	id := r.FormValue("id")
	if id == "" {
		writeError(w, r, 10, "missing parameter: id")
		return
	}

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

	// Delete songs first (no CASCADE without PRAGMA foreign_keys).
	err = sqlitex.ExecuteTransient(conn,
		`DELETE FROM playlist_song WHERE playlist_id = ?`,
		&sqlitex.ExecOptions{Args: []any{id}})
	if err != nil {
		sqlitex.ExecuteTransient(conn, "ROLLBACK", nil)
		writeError(w, r, 0, "database error")
		return
	}

	err = sqlitex.ExecuteTransient(conn,
		`DELETE FROM playlist WHERE id = ? AND user_id = ?`,
		&sqlitex.ExecOptions{Args: []any{id, username}})
	if err != nil {
		sqlitex.ExecuteTransient(conn, "ROLLBACK", nil)
		writeError(w, r, 0, "database error")
		return
	}

	if err := sqlitex.ExecuteTransient(conn, "COMMIT", nil); err != nil {
		writeError(w, r, 0, "database error")
		return
	}

	writeOK(w, r)
}

// resequencePlaylist renumbers positions to be contiguous starting from 0.
func resequencePlaylist(conn *sqlite.Conn, playlistID string) error {
	type entry struct {
		songID  string
		oldPos  int
	}
	var entries []entry
	err := sqlitex.ExecuteTransient(conn,
		`SELECT song_id, position FROM playlist_song WHERE playlist_id = ? ORDER BY position`,
		&sqlitex.ExecOptions{
			Args: []any{playlistID},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				entries = append(entries, entry{stmt.ColumnText(0), stmt.ColumnInt(1)})
				return nil
			},
		})
	if err != nil {
		return err
	}

	// Delete all and re-insert with new positions.
	err = sqlitex.ExecuteTransient(conn,
		`DELETE FROM playlist_song WHERE playlist_id = ?`,
		&sqlitex.ExecOptions{Args: []any{playlistID}})
	if err != nil {
		return err
	}
	for i, e := range entries {
		err = sqlitex.ExecuteTransient(conn,
			`INSERT INTO playlist_song (playlist_id, song_id, position) VALUES (?, ?, ?)`,
			&sqlitex.ExecOptions{Args: []any{playlistID, e.songID, i}})
		if err != nil {
			return err
		}
	}
	return nil
}
