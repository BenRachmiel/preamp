package api

import (
	"net/http"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func (s *Server) handleGetAlbumList2(w http.ResponseWriter, r *http.Request) {
	listType := r.FormValue("type")
	if listType == "" {
		writeError(w, r, 10, "missing parameter: type")
		return
	}

	size := intParam(r, "size", 10)
	if size > 500 {
		size = 500
	}
	offset := intParam(r, "offset", 0)

	conn, put, err := s.db.ReadConn()
	if err != nil {
		writeError(w, r, 0, "database error")
		return
	}
	defer put()

	var query string
	var args []any

	switch listType {
	case "random":
		query = `
			SELECT al.id, al.name, a.name, a.id, al.year, al.genre,
			       al.cover_art, al.song_count, al.duration, al.created_at
			FROM album al JOIN artist a ON a.id = al.artist_id
			ORDER BY RANDOM() LIMIT ?`
		args = []any{size}
	case "newest":
		query = `
			SELECT al.id, al.name, a.name, a.id, al.year, al.genre,
			       al.cover_art, al.song_count, al.duration, al.created_at
			FROM album al JOIN artist a ON a.id = al.artist_id
			ORDER BY al.created_at DESC LIMIT ? OFFSET ?`
		args = []any{size, offset}
	case "alphabeticalByName":
		query = `
			SELECT al.id, al.name, a.name, a.id, al.year, al.genre,
			       al.cover_art, al.song_count, al.duration, al.created_at
			FROM album al JOIN artist a ON a.id = al.artist_id
			ORDER BY al.name COLLATE NOCASE LIMIT ? OFFSET ?`
		args = []any{size, offset}
	case "alphabeticalByArtist":
		query = `
			SELECT al.id, al.name, a.name, a.id, al.year, al.genre,
			       al.cover_art, al.song_count, al.duration, al.created_at
			FROM album al JOIN artist a ON a.id = al.artist_id
			ORDER BY a.name COLLATE NOCASE, al.name COLLATE NOCASE LIMIT ? OFFSET ?`
		args = []any{size, offset}
	case "byYear":
		fromYear := intParam(r, "fromYear", 0)
		toYear := intParam(r, "toYear", 9999)
		query = `
			SELECT al.id, al.name, a.name, a.id, al.year, al.genre,
			       al.cover_art, al.song_count, al.duration, al.created_at
			FROM album al JOIN artist a ON a.id = al.artist_id
			WHERE al.year BETWEEN ? AND ?
			ORDER BY al.year LIMIT ? OFFSET ?`
		args = []any{fromYear, toYear, size, offset}
	case "byGenre":
		genre := r.FormValue("genre")
		if genre == "" {
			writeError(w, r, 10, "missing parameter: genre")
			return
		}
		query = `
			SELECT al.id, al.name, a.name, a.id, al.year, al.genre,
			       al.cover_art, al.song_count, al.duration, al.created_at
			FROM album al JOIN artist a ON a.id = al.artist_id
			WHERE al.genre = ?
			ORDER BY al.name COLLATE NOCASE LIMIT ? OFFSET ?`
		args = []any{genre, size, offset}
	case "starred":
		username, have := requireUsername(w, r)
		if !have {
			return
		}
		query = `
			SELECT al.id, al.name, a.name, a.id, al.year, al.genre,
			       al.cover_art, al.song_count, al.duration, al.created_at
			FROM star st
			JOIN album al ON al.id = st.item_id
			JOIN artist a ON a.id = al.artist_id
			WHERE st.user_id = ? AND st.item_type = ?
			ORDER BY st.created_at DESC LIMIT ? OFFSET ?`
		args = []any{username, itemTypeAlbum, size, offset}
	case "recent":
		username, have := requireUsername(w, r)
		if !have {
			return
		}
		query = `
			SELECT al.id, al.name, a.name, a.id, al.year, al.genre,
			       al.cover_art, al.song_count, al.duration, al.created_at
			FROM play_history ph
			JOIN song s ON s.id = ph.song_id
			JOIN album al ON al.id = s.album_id
			JOIN artist a ON a.id = al.artist_id
			WHERE ph.user_id = ?
			GROUP BY al.id
			ORDER BY MAX(ph.played_at) DESC LIMIT ? OFFSET ?`
		args = []any{username, size, offset}
	case "frequent":
		username, have := requireUsername(w, r)
		if !have {
			return
		}
		query = `
			SELECT al.id, al.name, a.name, a.id, al.year, al.genre,
			       al.cover_art, al.song_count, al.duration, al.created_at
			FROM play_history ph
			JOIN song s ON s.id = ph.song_id
			JOIN album al ON al.id = s.album_id
			JOIN artist a ON a.id = al.artist_id
			WHERE ph.user_id = ?
			GROUP BY al.id
			ORDER BY COUNT(*) DESC LIMIT ? OFFSET ?`
		args = []any{username, size, offset}
	default:
		writeError(w, r, 0, "unknown list type: "+listType)
		return
	}

	albums := []AlbumID3{}
	err = sqlitex.ExecuteTransient(conn, query, &sqlitex.ExecOptions{
		Args: args,
		ResultFunc: func(stmt *sqlite.Stmt) error {
			albums = append(albums, albumFromRow(stmt))
			return nil
		},
	})
	if err != nil {
		writeError(w, r, 0, "query error")
		return
	}

	resp := ok()
	resp.AlbumList2 = &AlbumList2{Albums: albums}
	writeResponse(w, r, resp)
}

func (s *Server) handleGetRandomSongs(w http.ResponseWriter, r *http.Request) {
	size := intParam(r, "size", 10)
	if size > 500 {
		size = 500
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
		FROM song s
		JOIN artist a ON a.id = s.artist_id
		JOIN album al ON al.id = s.album_id
		ORDER BY RANDOM() LIMIT ?
	`, &sqlitex.ExecOptions{
		Args: []any{size},
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
	resp.RandomSongs = &RandomSongs{Songs: songs}
	writeResponse(w, r, resp)
}

func (s *Server) handleGetStarred2(w http.ResponseWriter, r *http.Request) {
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

	result := Starred2{
		Artists: []ArtistID3{},
		Albums:  []AlbumID3{},
		Songs:   []SongID3{},
	}

	// Starred artists
	err = sqlitex.ExecuteTransient(conn, `
		SELECT a.id, a.name, COUNT(al.id) as album_count, st.created_at
		FROM star st
		JOIN artist a ON a.id = st.item_id
		LEFT JOIN album al ON al.artist_id = a.id
		WHERE st.user_id = ? AND st.item_type = ?
		GROUP BY a.id
		ORDER BY st.created_at DESC
	`, &sqlitex.ExecOptions{
		Args: []any{username, itemTypeArtist},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			result.Artists = append(result.Artists, ArtistID3{
				ID:         stmt.ColumnText(0),
				Name:       stmt.ColumnText(1),
				AlbumCount: stmt.ColumnInt(2),
			})
			return nil
		},
	})
	if err != nil {
		writeError(w, r, 0, "query error")
		return
	}

	// Starred albums
	err = sqlitex.ExecuteTransient(conn, `
		SELECT al.id, al.name, a.name, a.id, al.year, al.genre,
		       al.cover_art, al.song_count, al.duration, al.created_at
		FROM star st
		JOIN album al ON al.id = st.item_id
		JOIN artist a ON a.id = al.artist_id
		WHERE st.user_id = ? AND st.item_type = ?
		ORDER BY st.created_at DESC
	`, &sqlitex.ExecOptions{
		Args: []any{username, itemTypeAlbum},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			result.Albums = append(result.Albums, albumFromRow(stmt))
			return nil
		},
	})
	if err != nil {
		writeError(w, r, 0, "query error")
		return
	}

	// Starred songs
	err = sqlitex.ExecuteTransient(conn, `
		SELECT s.id, s.title, s.track, s.disc, s.year, s.genre,
		       s.duration, s.size, s.suffix, s.bitrate, s.content_type, s.path,
		       a.name, a.id, al.name, al.id, al.cover_art
		FROM star st
		JOIN song s ON s.id = st.item_id
		JOIN artist a ON a.id = s.artist_id
		JOIN album al ON al.id = s.album_id
		WHERE st.user_id = ? AND st.item_type = ?
		ORDER BY st.created_at DESC
	`, &sqlitex.ExecOptions{
		Args: []any{username, itemTypeSong},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			result.Songs = append(result.Songs, songFromRow(stmt))
			return nil
		},
	})
	if err != nil {
		writeError(w, r, 0, "query error")
		return
	}

	decorateRatings(conn, username, result.Songs)

	resp := ok()
	resp.Starred2 = &result
	writeResponse(w, r, resp)
}

func (s *Server) handleGetSongsByGenre(w http.ResponseWriter, r *http.Request) {
	genre := r.FormValue("genre")
	if genre == "" {
		writeError(w, r, 10, "missing parameter: genre")
		return
	}

	count := intParam(r, "count", 10)
	if count > 500 {
		count = 500
	}
	offset := intParam(r, "offset", 0)

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
		FROM song s
		JOIN artist a ON a.id = s.artist_id
		JOIN album al ON al.id = s.album_id
		WHERE s.genre = ?
		ORDER BY s.title COLLATE NOCASE
		LIMIT ? OFFSET ?
	`, &sqlitex.ExecOptions{
		Args: []any{genre, count, offset},
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
	resp.SongsByGenre = &SongsByGenre{Songs: songs}
	writeResponse(w, r, resp)
}
