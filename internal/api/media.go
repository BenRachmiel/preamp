package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/disintegration/imaging"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// handleStream serves audio, optionally slicing to a time range.
// Query params: startTime (seconds, float), duration (seconds, float).
// If both are present and the format supports native slicing, only that
// portion is returned. Otherwise the full file is served.

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	startStr := r.FormValue("startTime")
	durStr := r.FormValue("duration")

	if startStr != "" && durStr != "" {
		startTime, err1 := strconv.ParseFloat(startStr, 64)
		dur, err2 := strconv.ParseFloat(durStr, 64)
		if err1 == nil && err2 == nil && startTime >= 0 && dur > 0 {
			s.serveSlicedAudio(w, r, startTime, dur)
			return
		}
	}
	s.serveAudioFile(w, r)
}

// serveSlicedAudio attempts format-aware slicing; falls back to full-file serve.
func (s *Server) serveSlicedAudio(w http.ResponseWriter, r *http.Request, startTime, duration float64) {
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

	fileSize := stat.Size()
	ext := strings.ToLower(filepath.Ext(filePath))

	var handled bool
	switch ext {
	case ".mp3":
		handled = sliceMP3(w, f, fileSize, contentType, startTime, duration)
	case ".flac":
		handled = sliceFLAC(w, f, fileSize, contentType, startTime, duration)
	case ".wav":
		handled = sliceWAV(w, f, fileSize, contentType, startTime, duration)
	}

	if !handled {
		// Unsupported format or parse failure — serve full file.
		w.Header().Set("Content-Type", contentType)
		http.ServeContent(w, r, filePath, stat.ModTime(), f)
	}
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

const (
	minCoverArtSize = 32
	maxCoverArtSize = 1024
)

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

	servePath := coverPath

	// Handle size parameter — lazy resize with disk cache.
	if sizeStr := r.FormValue("size"); sizeStr != "" {
		size, parseErr := strconv.Atoi(sizeStr)
		if parseErr == nil && size > 0 {
			size = max(minCoverArtSize, min(size, maxCoverArtSize))
			resized, resizeErr := s.resizedCoverArt(coverPath, size)
			if resizeErr != nil {
				s.log.Error("resizing cover art", "path", coverPath, "size", size, "err", resizeErr)
			} else {
				servePath = resized
			}
		}
	}

	f, err := os.Open(servePath)
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
	http.ServeContent(w, r, servePath, stat.ModTime(), f)
}

// resizedCoverArt returns the path to a resized cover art image, creating it
// on demand if it doesn't exist. Cached as {base}_{size}.jpg alongside the original.
// Uses atomic write (temp file + rename) to avoid serving corrupt files on concurrent requests.
func (s *Server) resizedCoverArt(originalPath string, size int) (string, error) {
	dir := filepath.Dir(originalPath)
	base := strings.TrimSuffix(filepath.Base(originalPath), filepath.Ext(originalPath))
	resizedPath := filepath.Join(dir, fmt.Sprintf("%s_%d.jpg", base, size))

	// Return cached version if it exists.
	if _, err := os.Stat(resizedPath); err == nil {
		return resizedPath, nil
	}

	src, err := imaging.Open(originalPath)
	if err != nil {
		return "", fmt.Errorf("opening image: %w", err)
	}

	resized := imaging.Fit(src, size, size, imaging.CatmullRom)

	// Write to temp file, then atomic rename to avoid serving partial writes.
	tmp, err := os.CreateTemp(dir, base+"_*.tmp")
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if err := imaging.Encode(tmp, resized, imaging.JPEG); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("encoding resized image: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, resizedPath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("renaming resized image: %w", err)
	}

	return resizedPath, nil
}
