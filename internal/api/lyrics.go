package api

import (
	"net/http"
	"os"

	"github.com/BenRachmiel/preamp/internal/scanner"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func (s *Server) handleGetLyricsBySongId(w http.ResponseWriter, r *http.Request) {
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

	var filePath string
	found := false

	err = sqlitex.ExecuteTransient(conn, `SELECT path FROM song WHERE id = ?`, &sqlitex.ExecOptions{
		Args: []any{id},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			filePath = stmt.ColumnText(0)
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
		s.log.Error("opening file for lyrics", "path", filePath, "err", err)
		// Return empty lyrics list rather than error — file may have been moved
		resp := ok()
		resp.LyricsList = &LyricsList{}
		writeResponse(w, r, resp)
		return
	}
	defer f.Close()

	lyricFrames, err := scanner.ReadID3v2Lyrics(f)
	if err != nil {
		s.log.Error("reading lyrics", "path", filePath, "err", err)
		resp := ok()
		resp.LyricsList = &LyricsList{}
		writeResponse(w, r, resp)
		return
	}

	resp := ok()
	resp.LyricsList = toLyricsList(lyricFrames)
	writeResponse(w, r, resp)
}

func toLyricsList(frames []scanner.LyricFrame) *LyricsList {
	if len(frames) == 0 {
		return &LyricsList{}
	}

	structured := make([]StructuredLyrics, 0, len(frames))
	for _, f := range frames {
		lines := make([]LyricLine, 0, len(f.Lines))
		for _, l := range f.Lines {
			line := LyricLine{Value: l.Value}
			if f.Synced {
				ms := l.Start
				line.Start = &ms
			}
			lines = append(lines, line)
		}
		structured = append(structured, StructuredLyrics{
			Lang:   f.Lang,
			Synced: f.Synced,
			Lines:  lines,
		})
	}
	return &LyricsList{StructuredLyrics: structured}
}
