package api

import (
	"net/http"
	"os"
	"strings"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	s.serveAudioFile(w, r)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	s.serveAudioFile(w, r)
}

func (s *Server) serveAudioFile(w http.ResponseWriter, r *http.Request) {
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

	var filePath, contentType string
	found := false

	err = sqlitex.ExecuteTransient(conn, `SELECT path, content_type FROM song WHERE id = ?`, &sqlitex.ExecOptions{
		Args: []any{id},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			filePath = stmt.ColumnText(0)
			contentType = stmt.ColumnText(1)
			found = true
			return nil
		},
	})
	if err != nil || !found {
		writeError(w, r, 70, "song not found")
		return
	}

	f, err := os.Open(filePath)
	if err != nil {
		s.log.Error("opening audio file", "path", filePath, "err", err)
		writeError(w, r, 0, "file not accessible")
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		writeError(w, r, 0, "file not accessible")
		return
	}

	w.Header().Set("Content-Type", contentType)
	// http.ServeContent handles Range requests, Content-Length, sendfile.
	http.ServeContent(w, r, filePath, stat.ModTime(), f)
}

const coverArtPrefix = "al-"

func (s *Server) handleGetCoverArt(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	if id == "" {
		writeError(w, r, 10, "missing parameter: id")
		return
	}

	// Cover art IDs are album IDs or "al-{albumID}" prefixed.
	albumID := strings.TrimPrefix(id, coverArtPrefix)

	conn, put, err := s.db.ReadConn()
	if err != nil {
		writeError(w, r, 0, "database error")
		return
	}
	defer put()

	var coverPath string
	found := false

	err = sqlitex.ExecuteTransient(conn, `SELECT cover_art FROM album WHERE id = ?`, &sqlitex.ExecOptions{
		Args: []any{albumID},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			coverPath = stmt.ColumnText(0)
			found = true
			return nil
		},
	})
	if err != nil || !found || coverPath == "" {
		http.NotFound(w, r)
		return
	}

	f, err := os.Open(coverPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, coverPath, stat.ModTime(), f)
}
