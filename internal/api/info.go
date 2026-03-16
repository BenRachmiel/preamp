package api

import (
	"net/http"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func (s *Server) handleGetArtistInfo2(w http.ResponseWriter, r *http.Request) {
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

	found := false
	if err := sqlitex.ExecuteTransient(conn,
		`SELECT 1 FROM artist WHERE id = ?`,
		&sqlitex.ExecOptions{
			Args: []any{id},
			ResultFunc: func(stmt *sqlite.Stmt) error { found = true; return nil },
		}); err != nil {
		writeError(w, r, 0, "database error")
		return
	}
	if !found {
		writeError(w, r, 70, "artist not found")
		return
	}

	resp := ok()
	resp.ArtistInfo2 = &ArtistInfo2{SimilarArtist: []ArtistID3{}}
	writeResponse(w, r, resp)
}

func (s *Server) handleGetAlbumInfo2(w http.ResponseWriter, r *http.Request) {
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

	found := false
	if err := sqlitex.ExecuteTransient(conn,
		`SELECT 1 FROM album WHERE id = ?`,
		&sqlitex.ExecOptions{
			Args: []any{id},
			ResultFunc: func(stmt *sqlite.Stmt) error { found = true; return nil },
		}); err != nil {
		writeError(w, r, 0, "database error")
		return
	}
	if !found {
		writeError(w, r, 70, "album not found")
		return
	}

	resp := ok()
	resp.AlbumInfo = &AlbumInfo{}
	writeResponse(w, r, resp)
}

func (s *Server) handleGetSimilarSongs2(w http.ResponseWriter, r *http.Request) {
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

	found := false
	if err := sqlitex.ExecuteTransient(conn,
		`SELECT 1 FROM song WHERE id = ?`,
		&sqlitex.ExecOptions{
			Args: []any{id},
			ResultFunc: func(stmt *sqlite.Stmt) error { found = true; return nil },
		}); err != nil {
		writeError(w, r, 0, "database error")
		return
	}
	if !found {
		writeError(w, r, 70, "song not found")
		return
	}

	resp := ok()
	resp.SimilarSongs2 = &SimilarSongs2{Songs: []SongID3{}}
	writeResponse(w, r, resp)
}

func (s *Server) handleGetTopSongs(w http.ResponseWriter, r *http.Request) {
	artistName := r.FormValue("artist")
	if artistName == "" {
		writeError(w, r, 10, "missing parameter: artist")
		return
	}

	count := intParam(r, "count", 50)
	if count > 500 {
		count = 500
	}

	conn, put, err := s.db.ReadConn()
	if err != nil {
		writeError(w, r, 0, "database error")
		return
	}
	defer put()

	songs := []SongID3{}
	err = sqlitex.ExecuteTransient(conn, `
		SELECT s.id, s.title, s.track, s.disc, s.year, s.genre,
		       s.duration, s.size, s.suffix, s.bitrate, s.content_type, s.path,
		       a.name, a.id, al.name, al.id, al.cover_art
		FROM play_history ph
		JOIN song s ON s.id = ph.song_id
		JOIN artist a ON a.id = s.artist_id
		JOIN album al ON al.id = s.album_id
		WHERE a.name = ?
		GROUP BY s.id
		ORDER BY COUNT(*) DESC LIMIT ?
	`, &sqlitex.ExecOptions{
		Args: []any{artistName, count},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			songs = append(songs, songFromRow(stmt))
			return nil
		},
	})
	if err != nil {
		writeError(w, r, 0, "query error")
		return
	}

	username := usernameFromRequest(r)
	decorateRatings(conn, username, songs)

	resp := ok()
	resp.TopSongs = &TopSongs{Songs: songs}
	writeResponse(w, r, resp)
}
