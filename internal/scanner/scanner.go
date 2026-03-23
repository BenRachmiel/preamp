package scanner

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/dhowden/tag"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/BenRachmiel/preamp/internal/db"
)

var supportedExts = map[string]string{
	".mp3":  "audio/mpeg",
	".flac": "audio/flac",
	".ogg":  "audio/ogg",
	".m4a":  "audio/mp4",
	".opus": "audio/opus",
	".wma":  "audio/x-ms-wma",
	".wav":  "audio/wav",
	".aac":  "audio/aac",
}

type Scanner struct {
	db       *db.DB
	musicDir string
	log      *slog.Logger
	scanning atomic.Bool
	count    atomic.Int64
}

func New(database *db.DB, musicDir string, log *slog.Logger) *Scanner {
	// Resolve to real path for canonical base (symlink-safe).
	if resolved, err := filepath.EvalSymlinks(musicDir); err == nil {
		musicDir = resolved
	}
	return &Scanner{
		db:       database,
		musicDir: musicDir,
		log:      log,
	}
}

func (s *Scanner) Scanning() bool {
	return s.scanning.Load()
}

func (s *Scanner) Count() int {
	return int(s.count.Load())
}

// Run performs a full scan of the music directory. Tracks are processed in
// streaming batches to bound peak memory to O(batchSize) instead of
// O(totalTracks).
func (s *Scanner) Run() error {
	if !s.scanning.CompareAndSwap(false, true) {
		return fmt.Errorf("scan already in progress")
	}
	defer s.scanning.Store(false)
	s.count.Store(0)

	s.log.Info("starting scan", "dir", s.musicDir)

	conn, put, err := s.db.WriteConn()
	if err != nil {
		return fmt.Errorf("getting write conn: %w", err)
	}
	defer put()

	const batchSize = 1000
	batch := make([]trackInfo, 0, batchSize)

	// Collect folder art discovered during walk: dir → art file path.
	folderArt := make(map[string]string)

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := s.insertBatch(conn, batch); err != nil {
			return fmt.Errorf("inserting batch: %w", err)
		}
		batch = batch[:0] // reuse backing array
		return nil
	}

	err = filepath.WalkDir(s.musicDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			s.log.Warn("walk error", "path", path, "err", err)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		name := strings.ToLower(d.Name())
		dir := filepath.Dir(path)

		// Check if this file is folder art before checking audio extensions.
		if _, seen := folderArt[dir]; !seen {
			for _, artName := range folderArtNames {
				if name == artName {
					folderArt[dir] = path
					return nil
				}
			}
		}

		ext := strings.ToLower(filepath.Ext(path))
		contentType, ok := supportedExts[ext]
		if !ok {
			return nil
		}

		info, err := safeReadTrack(path, ext, contentType, s.log)
		if err != nil {
			s.log.Warn("reading track", "path", path, "err", err)
			return nil
		}

		batch = append(batch, info)
		s.count.Add(1)
		if len(batch) >= batchSize {
			if err := flush(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walking music dir: %w", err)
	}

	// Flush remaining tracks.
	if err := flush(); err != nil {
		return err
	}

	// Assign folder art to albums and clear stale cover_art paths.
	if err := s.resolveAlbumArt(conn, folderArt); err != nil {
		s.log.Warn("album art resolution", "err", err)
	}

	// Update album aggregates.
	if err := s.updateAlbumStats(conn); err != nil {
		return fmt.Errorf("updating album stats: %w", err)
	}

	// Rebuild FTS5 index.
	if err := s.rebuildFTS(conn); err != nil {
		return fmt.Errorf("rebuilding FTS index: %w", err)
	}

	s.log.Info("scan complete", "tracks", s.count.Load())
	return nil
}

type trackInfo struct {
	path        string
	ext         string // without dot
	contentType string
	size        int64
	title       string
	artist      string
	album       string
	genre       string
	year        int
	track       int
	disc        int
	duration    int // seconds
	bitrate     int // kbps
}

func safeReadTrack(path, ext, contentType string, log *slog.Logger) (info trackInfo, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic reading tags: %v", r)
		}
	}()
	return readTrack(path, ext, contentType, log)
}

func readTrack(path, ext, contentType string, log *slog.Logger) (trackInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return trackInfo{}, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return trackInfo{}, err
	}

	info := trackInfo{
		path:        path,
		ext:         strings.TrimPrefix(ext, "."),
		contentType: contentType,
		size:        stat.Size(),
	}

	meta, err := tag.ReadFrom(f)
	if err != nil {
		// No tags — use filename as title.
		info.title = strings.TrimSuffix(filepath.Base(path), ext)
		info.artist = "Unknown Artist"
		info.album = filepath.Base(filepath.Dir(path))
	} else {
		info.title = meta.Title()
		if info.title == "" {
			info.title = strings.TrimSuffix(filepath.Base(path), ext)
		}
		info.artist = meta.Artist()
		if info.artist == "" {
			info.artist = "Unknown Artist"
		}
		info.album = meta.Album()
		if info.album == "" {
			info.album = filepath.Base(filepath.Dir(path))
		}
		info.genre = meta.Genre()
		info.year = meta.Year()
		t, _ := meta.Track()
		info.track = t
		d, _ := meta.Disc()
		info.disc = d
	}

	// Parse duration from the same open file handle (avoids a second open).
	dur, br := parseDuration(f, stat.Size(), ext, log)
	if dur > 0 {
		info.duration = dur
		info.bitrate = br
	}

	return info, nil
}

func (s *Scanner) insertBatch(conn *sqlite.Conn, tracks []trackInfo) error {
	endFn, err := sqlitex.ImmediateTransaction(conn)
	if err != nil {
		return err
	}
	defer endFn(&err)

	// Cache artist/album IDs to avoid repeated lookups within batch.
	artistIDs := make(map[string]string) // artist name → id
	albumIDs := make(map[string]string)  // "artist\x00album" → id

	for _, t := range tracks {
		// Upsert artist.
		artistID, ok := artistIDs[t.artist]
		if !ok {
			artistID, err = s.upsertArtist(conn, t.artist)
			if err != nil {
				return err
			}
			artistIDs[t.artist] = artistID
		}

		// Upsert album.
		albumKey := t.artist + "\x00" + t.album
		albumID, ok := albumIDs[albumKey]
		if !ok {
			albumID, err = s.upsertAlbum(conn, artistID, t.album, t.year, t.genre)
			if err != nil {
				return err
			}
			albumIDs[albumKey] = albumID
		}

		// Upsert song (by path).
		err = s.upsertSong(conn, t, artistID, albumID)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *Scanner) upsertArtist(conn *sqlite.Conn, name string) (string, error) {
	var id string
	err := sqlitex.ExecuteTransient(conn, `SELECT id FROM artist WHERE name = ?`, &sqlitex.ExecOptions{
		Args: []any{name},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			id = stmt.ColumnText(0)
			return nil
		},
	})
	if err != nil {
		return "", err
	}
	if id != "" {
		return id, nil
	}

	id = db.NewID()
	err = sqlitex.ExecuteTransient(conn, `INSERT INTO artist (id, name) VALUES (?, ?)`, &sqlitex.ExecOptions{
		Args: []any{id, name},
	})
	return id, err
}

func (s *Scanner) upsertAlbum(conn *sqlite.Conn, artistID, name string, year int, genre string) (string, error) {
	var id string
	err := sqlitex.ExecuteTransient(conn, `SELECT id FROM album WHERE artist_id = ? AND name = ?`, &sqlitex.ExecOptions{
		Args: []any{artistID, name},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			id = stmt.ColumnText(0)
			return nil
		},
	})
	if err != nil {
		return "", err
	}
	if id != "" {
		return id, nil
	}

	id = db.NewID()
	err = sqlitex.ExecuteTransient(conn, `
		INSERT INTO album (id, artist_id, name, year, genre) VALUES (?, ?, ?, ?, ?)
	`, &sqlitex.ExecOptions{
		Args: []any{id, artistID, name, year, genre},
	})
	return id, err
}

func (s *Scanner) upsertSong(conn *sqlite.Conn, t trackInfo, artistID, albumID string) error {
	id := db.NewID()
	return sqlitex.ExecuteTransient(conn, `
		INSERT INTO song (id, album_id, artist_id, title, track, disc, year, genre,
		                   duration, size, suffix, bitrate, content_type, path)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			title=excluded.title, track=excluded.track, disc=excluded.disc,
			year=excluded.year, genre=excluded.genre, duration=excluded.duration,
			size=excluded.size, suffix=excluded.suffix, bitrate=excluded.bitrate,
			content_type=excluded.content_type, artist_id=excluded.artist_id,
			album_id=excluded.album_id
	`, &sqlitex.ExecOptions{
		Args: []any{
			id, albumID, artistID, t.title, t.track, t.disc, t.year, t.genre,
			t.duration, t.size, t.ext, t.bitrate, t.contentType, t.path,
		},
	})
}

func (s *Scanner) updateAlbumStats(conn *sqlite.Conn) error {
	return sqlitex.ExecuteTransient(conn, `
		UPDATE album SET song_count = agg.cnt, duration = agg.dur
		FROM (
			SELECT album_id, COUNT(*) AS cnt, COALESCE(SUM(duration), 0) AS dur
			FROM song GROUP BY album_id
		) agg
		WHERE album.id = agg.album_id
	`, nil)
}

// folderArtNames lists common cover art filenames to look for in album directories.
var folderArtNames = []string{
	"cover.jpg", "cover.png",
	"folder.jpg", "folder.png",
	"front.jpg", "front.png",
}

// resolveAlbumArt assigns folder art to albums missing cover art and clears
// stale cover_art paths that no longer exist on disk.
func (s *Scanner) resolveAlbumArt(conn *sqlite.Conn, folderArt map[string]string) error {
	type albumInfo struct {
		id       string
		coverArt string // existing cover_art value (may be empty)
		songDir  string // directory of a sample song
	}
	var albums []albumInfo

	err := sqlitex.ExecuteTransient(conn, `
		SELECT a.id, COALESCE(a.cover_art, ''), MIN(s.path) FROM album a
		JOIN song s ON s.album_id = a.id
		GROUP BY a.id
	`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			albums = append(albums, albumInfo{
				id:       stmt.ColumnText(0),
				coverArt: stmt.ColumnText(1),
				songDir:  filepath.Dir(stmt.ColumnText(2)),
			})
			return nil
		},
	})
	if err != nil {
		return err
	}

	assigned, cleared := 0, 0
	for _, album := range albums {
		if album.coverArt != "" {
			// Verify existing cover art still exists.
			if _, err := os.Stat(album.coverArt); err != nil {
				_ = sqlitex.ExecuteTransient(conn, `UPDATE album SET cover_art = NULL WHERE id = ?`, &sqlitex.ExecOptions{
					Args: []any{album.id},
				})
				cleared++
				album.coverArt = ""
			}
		}

		if album.coverArt == "" {
			if artPath, ok := folderArt[album.songDir]; ok {
				_ = sqlitex.ExecuteTransient(conn, `UPDATE album SET cover_art = ? WHERE id = ?`, &sqlitex.ExecOptions{
					Args: []any{artPath, album.id},
				})
				assigned++
			}
		}
	}

	if assigned > 0 || cleared > 0 {
		s.log.Info("album art resolved", "assigned", assigned, "cleared_stale", cleared)
	}
	return nil
}

func (s *Scanner) rebuildFTS(conn *sqlite.Conn) error {
	// Contentless FTS5 tables don't support DELETE. Drop and recreate.
	// Wrap in a transaction so concurrent reads see either the old or new
	// table, never a missing one.
	endFn, err := sqlitex.ImmediateTransaction(conn)
	if err != nil {
		return err
	}
	defer endFn(&err)

	if err = sqlitex.ExecuteTransient(conn, `DROP TABLE IF EXISTS song_fts`, nil); err != nil {
		return err
	}
	if err = sqlitex.ExecuteTransient(conn, `
		CREATE VIRTUAL TABLE song_fts USING fts5(
			title, artist_name, album_name,
			content='',
			tokenize='unicode61 remove_diacritics 2'
		)
	`, nil); err != nil {
		return err
	}
	return sqlitex.ExecuteTransient(conn, `
		INSERT INTO song_fts(rowid, title, artist_name, album_name)
		SELECT s.rowid, s.title, a.name, al.name
		FROM song s
		JOIN artist a ON a.id = s.artist_id
		JOIN album al ON al.id = s.album_id
	`, nil)
}
