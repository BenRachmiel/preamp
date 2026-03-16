package api

import (
	"net/http"
	"slices"
	"strings"
	"unicode"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func (s *Server) handleGetArtists(w http.ResponseWriter, r *http.Request) {
	conn, put, err := s.db.ReadConn()
	if err != nil {
		writeError(w, r, 0, "database error")
		return
	}
	defer put()

	indexMap := make(map[string][]ArtistID3)

	err = sqlitex.ExecuteTransient(conn, `
		SELECT a.id, a.name, COUNT(al.id) as album_count
		FROM artist a
		LEFT JOIN album al ON al.artist_id = a.id
		GROUP BY a.id
		ORDER BY a.name COLLATE NOCASE
	`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			artist := ArtistID3{
				ID:         stmt.ColumnText(0),
				Name:       stmt.ColumnText(1),
				AlbumCount: stmt.ColumnInt(2),
			}
			letter := indexLetter(artist.Name)
			indexMap[letter] = append(indexMap[letter], artist)
			return nil
		},
	})
	if err != nil {
		writeError(w, r, 0, "query error")
		return
	}

	indices := []IndexID3{}
	for letter, artists := range indexMap {
		indices = append(indices, IndexID3{Name: letter, Artists: artists})
	}
	sortIndices(indices)

	// Move "#" to end if present.
	if len(indices) > 0 && indices[0].Name == "#" {
		indices = append(indices[1:], indices[0])
	}

	resp := ok()
	resp.Artists = &ArtistsID3{Index: indices}
	writeResponse(w, r, resp)
}

func (s *Server) handleGetArtist(w http.ResponseWriter, r *http.Request) {
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

	var artist ArtistWithAlbumsID3
	found := false

	err = sqlitex.ExecuteTransient(conn, `SELECT id, name FROM artist WHERE id = ?`, &sqlitex.ExecOptions{
		Args: []any{id},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			artist.ID = stmt.ColumnText(0)
			artist.Name = stmt.ColumnText(1)
			found = true
			return nil
		},
	})
	if err != nil || !found {
		writeError(w, r, 70, "artist not found")
		return
	}

	err = sqlitex.ExecuteTransient(conn, `
		SELECT al.id, al.name, ?, ?, al.year, al.genre,
		       al.cover_art, al.song_count, al.duration, al.created_at
		FROM album al WHERE al.artist_id = ? ORDER BY al.year, al.name
	`, &sqlitex.ExecOptions{
		Args: []any{artist.Name, artist.ID, id},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			artist.Albums = append(artist.Albums, albumFromRow(stmt))
			return nil
		},
	})
	if err != nil {
		writeError(w, r, 0, "query error")
		return
	}

	artist.AlbumCount = len(artist.Albums)
	resp := ok()
	resp.Artist = &artist
	writeResponse(w, r, resp)
}

func (s *Server) handleGetAlbum(w http.ResponseWriter, r *http.Request) {
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

	var album AlbumWithSongsID3
	found := false

	err = sqlitex.ExecuteTransient(conn, `
		SELECT al.id, al.name, a.name, a.id, al.year, al.genre,
		       al.cover_art, al.song_count, al.duration, al.created_at
		FROM album al
		JOIN artist a ON a.id = al.artist_id
		WHERE al.id = ?
	`, &sqlitex.ExecOptions{
		Args: []any{id},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			al := albumFromRow(stmt)
			album.ID = al.ID
			album.Name = al.Name
			album.Artist = al.Artist
			album.ArtistID = al.ArtistID
			album.Year = al.Year
			album.Genre = al.Genre
			album.CoverArt = al.CoverArt
			album.SongCount = al.SongCount
			album.Duration = al.Duration
			album.Created = al.Created
			found = true
			return nil
		},
	})
	if err != nil || !found {
		writeError(w, r, 70, "album not found")
		return
	}

	err = sqlitex.ExecuteTransient(conn, `
		SELECT s.id, s.title, s.track, s.disc, s.year, s.genre,
		       s.duration, s.size, s.suffix, s.bitrate, s.content_type, s.path,
		       a.name, a.id, al.name, al.id, al.cover_art
		FROM song s
		JOIN artist a ON a.id = s.artist_id
		JOIN album al ON al.id = s.album_id
		WHERE s.album_id = ?
		ORDER BY s.disc, s.track
	`, &sqlitex.ExecOptions{
		Args: []any{id},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			song := songFromRow(stmt)
			album.Songs = append(album.Songs, song)
			return nil
		},
	})
	if err != nil {
		writeError(w, r, 0, "query error")
		return
	}

	username := usernameFromRequest(r)
	decorateRatings(conn, username, album.Songs)

	resp := ok()
	resp.Album = &album
	writeResponse(w, r, resp)
}

func (s *Server) handleGetSong(w http.ResponseWriter, r *http.Request) {
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

	var song SongID3
	found := false

	err = sqlitex.ExecuteTransient(conn, `
		SELECT s.id, s.title, s.track, s.disc, s.year, s.genre,
		       s.duration, s.size, s.suffix, s.bitrate, s.content_type, s.path,
		       a.name, a.id, al.name, al.id, al.cover_art
		FROM song s
		JOIN artist a ON a.id = s.artist_id
		JOIN album al ON al.id = s.album_id
		WHERE s.id = ?
	`, &sqlitex.ExecOptions{
		Args: []any{id},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			song = songFromRow(stmt)
			found = true
			return nil
		},
	})
	if err != nil || !found {
		writeError(w, r, 70, "song not found")
		return
	}

	username := usernameFromRequest(r)
	songs := []SongID3{song}
	decorateRatings(conn, username, songs)
	song = songs[0]

	resp := ok()
	resp.Song = &song
	writeResponse(w, r, resp)
}

func (s *Server) handleGetGenres(w http.ResponseWriter, r *http.Request) {
	conn, put, err := s.db.ReadConn()
	if err != nil {
		writeError(w, r, 0, "database error")
		return
	}
	defer put()

	genres := []Genre{}
	err = sqlitex.ExecuteTransient(conn, `
		SELECT genre, COUNT(*) as song_count, COUNT(DISTINCT album_id) as album_count
		FROM song
		WHERE genre != ''
		GROUP BY genre
		ORDER BY genre
	`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			genres = append(genres, Genre{
				Name:       stmt.ColumnText(0),
				SongCount:  stmt.ColumnInt(1),
				AlbumCount: stmt.ColumnInt(2),
			})
			return nil
		},
	})
	if err != nil {
		writeError(w, r, 0, "query error")
		return
	}

	resp := ok()
	resp.Genres = &Genres{Genres: genres}
	writeResponse(w, r, resp)
}

// albumFromRow maps a standard 10-column album query result to AlbumID3.
// Column order: al.id, al.name, a.name, a.id, al.year, al.genre,
//
//	al.cover_art, al.song_count, al.duration, al.created_at
func albumFromRow(stmt *sqlite.Stmt) AlbumID3 {
	return AlbumID3{
		ID:        stmt.ColumnText(0),
		Name:      stmt.ColumnText(1),
		Artist:    stmt.ColumnText(2),
		ArtistID:  stmt.ColumnText(3),
		Year:      stmt.ColumnInt(4),
		Genre:     stmt.ColumnText(5),
		CoverArt:  stmt.ColumnText(6),
		SongCount: stmt.ColumnInt(7),
		Duration:  stmt.ColumnInt(8),
		Created:   stmt.ColumnText(9),
	}
}

// songFromRow maps a standard 17-column song query result to SongID3.
// Column order: s.id, s.title, s.track, s.disc, s.year, s.genre,
//
//	s.duration, s.size, s.suffix, s.bitrate, s.content_type, s.path,
//	a.name, a.id, al.name, al.id, al.cover_art
func songFromRow(stmt *sqlite.Stmt) SongID3 {
	return SongID3{
		ID:          stmt.ColumnText(0),
		Title:       stmt.ColumnText(1),
		Track:       stmt.ColumnInt(2),
		Disc:        stmt.ColumnInt(3),
		Year:        stmt.ColumnInt(4),
		Genre:       stmt.ColumnText(5),
		Duration:    stmt.ColumnInt(6),
		Size:        stmt.ColumnInt64(7),
		Suffix:      stmt.ColumnText(8),
		BitRate:     stmt.ColumnInt(9),
		ContentType: stmt.ColumnText(10),
		Path:        stmt.ColumnText(11),
		Artist:      stmt.ColumnText(12),
		ArtistID:    stmt.ColumnText(13),
		Album:       stmt.ColumnText(14),
		AlbumID:     stmt.ColumnText(15),
		CoverArt:    stmt.ColumnText(16),
		Type:        "music",
	}
}

func indexLetter(name string) string {
	if name == "" {
		return "#"
	}
	r := unicode.ToUpper([]rune(name)[0])
	if unicode.IsLetter(r) {
		return string(r)
	}
	return "#"
}

func sortIndices(indices []IndexID3) {
	slices.SortFunc(indices, func(a, b IndexID3) int {
		return strings.Compare(a.Name, b.Name)
	})
}
