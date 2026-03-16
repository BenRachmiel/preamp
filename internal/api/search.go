package api

import (
	"net/http"
	"strconv"
	"strings"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func (s *Server) handleSearch3(w http.ResponseWriter, r *http.Request) {
	query := r.FormValue("query")
	if query == "" {
		writeError(w, r, 10, "missing parameter: query")
		return
	}

	artistCount := intParam(r, "artistCount", 20)
	albumCount := intParam(r, "albumCount", 20)
	songCount := intParam(r, "songCount", 20)

	conn, put, err := s.db.ReadConn()
	if err != nil {
		writeError(w, r, 0, "database error")
		return
	}
	defer put()

	result := SearchResult3{
		Artists: []ArtistID3{},
		Albums:  []AlbumID3{},
		Songs:   []SongID3{},
	}
	likeQuery := "%" + query + "%"

	// Search artists
	err = sqlitex.ExecuteTransient(conn, `
		SELECT a.id, a.name, COUNT(al.id) as album_count
		FROM artist a
		LEFT JOIN album al ON al.artist_id = a.id
		WHERE a.name LIKE ?
		GROUP BY a.id
		ORDER BY a.name COLLATE NOCASE
		LIMIT ?
	`, &sqlitex.ExecOptions{
		Args: []any{likeQuery, artistCount},
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

	// Search albums
	err = sqlitex.ExecuteTransient(conn, `
		SELECT al.id, al.name, a.name, a.id, al.year, al.genre,
		       al.cover_art, al.song_count, al.duration, al.created_at
		FROM album al
		JOIN artist a ON a.id = al.artist_id
		WHERE al.name LIKE ?
		ORDER BY al.name COLLATE NOCASE
		LIMIT ?
	`, &sqlitex.ExecOptions{
		Args: []any{likeQuery, albumCount},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			result.Albums = append(result.Albums, albumFromRow(stmt))
			return nil
		},
	})
	if err != nil {
		writeError(w, r, 0, "query error")
		return
	}

	// Search songs via FTS5 index.
	ftsQuery := ftsEscape(query) + "*"
	err = sqlitex.ExecuteTransient(conn, `
		SELECT s.id, s.title, s.track, s.disc, s.year, s.genre,
		       s.duration, s.size, s.suffix, s.bitrate, s.content_type, s.path,
		       a.name, a.id, al.name, al.id, al.cover_art
		FROM song_fts fts
		JOIN song s ON s.rowid = fts.rowid
		JOIN artist a ON a.id = s.artist_id
		JOIN album al ON al.id = s.album_id
		WHERE song_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, &sqlitex.ExecOptions{
		Args: []any{ftsQuery, songCount},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			result.Songs = append(result.Songs, songFromRow(stmt))
			return nil
		},
	})
	if err != nil {
		writeError(w, r, 0, "query error")
		return
	}

	username := usernameFromRequest(r)
	decorateRatings(conn, username, result.Songs)

	resp := ok()
	resp.SearchResult3 = &result
	writeResponse(w, r, resp)
}

// ftsEscape wraps each token in double quotes to prevent FTS5 syntax errors
// from user input containing special characters.
func ftsEscape(query string) string {
	return "\"" + strings.ReplaceAll(query, "\"", "\"\"") + "\""
}

func intParam(r *http.Request, name string, fallback int) int {
	v := r.FormValue(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}
