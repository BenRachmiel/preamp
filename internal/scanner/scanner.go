package scanner

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

// scanWorkers is the number of concurrent file-reading goroutines.
// Kept small to avoid overwhelming NFS but enough to hide per-file latency.
var scanWorkers = min(4, runtime.NumCPU())

// trackJob carries a discovered audio file from the walker to a parse worker.
type trackJob struct {
	path        string
	ext         string
	contentType string
}

// Run performs a full scan of the music directory using a pipeline:
//
//	walker → parse workers (concurrent I/O) → DB writer (serialized batches)
//
// This hides NFS latency behind parallel file reads while keeping SQLite
// writes serialized on the single write connection.
func (s *Scanner) Run() error {
	if !s.scanning.CompareAndSwap(false, true) {
		return fmt.Errorf("scan already in progress")
	}
	defer s.scanning.Store(false)
	s.count.Store(0)

	s.log.Info("starting scan", "dir", s.musicDir, "workers", scanWorkers)

	conn, put, err := s.db.WriteConn()
	if err != nil {
		return fmt.Errorf("getting write conn: %w", err)
	}
	defer put()

	// Collect folder art discovered during walk: dir → art file path.
	folderArt := make(map[string]string)

	// Stage 1: walker sends jobs to parse workers.
	jobs := make(chan trackJob, 100)

	// Stage 2: parse workers send results to DB writer.
	results := make(chan trackInfo, 100)

	// --- Parse workers ---
	var parseWg sync.WaitGroup
	parseWg.Add(scanWorkers)
	for range scanWorkers {
		go func() {
			defer parseWg.Done()
			for job := range jobs {
				info, err := readTrack(job.path, job.ext, job.contentType, s.log)
				if err != nil {
					s.log.Warn("reading track", "path", job.path, "err", err)
					continue
				}
				results <- info
			}
		}()
	}

	// Close results channel once all workers finish.
	go func() {
		parseWg.Wait()
		close(results)
	}()

	// --- DB writer (single goroutine) ---
	const batchSize = 1000
	var writerErr error
	var writerWg sync.WaitGroup
	writerWg.Add(1)
	go func() {
		defer writerWg.Done()
		batch := make([]trackInfo, 0, batchSize)
		for info := range results {
			batch = append(batch, info)
			s.count.Add(1)
			if len(batch) >= batchSize {
				if err := s.insertBatch(conn, batch); err != nil {
					writerErr = fmt.Errorf("inserting batch: %w", err)
					// Drain remaining results so workers don't block.
					for range results {
					}
					return
				}
				batch = batch[:0]
			}
		}
		// Flush remaining.
		if len(batch) > 0 {
			if err := s.insertBatch(conn, batch); err != nil {
				writerErr = fmt.Errorf("inserting batch: %w", err)
			}
		}
	}()

	// --- Walker (runs in current goroutine) ---
	walkErr := filepath.WalkDir(s.musicDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			s.log.Warn("walk error", "path", path, "err", err)
			return nil
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if contentType, ok := supportedExts[ext]; ok {
			jobs <- trackJob{path: path, ext: ext, contentType: contentType}
			return nil
		}

		// Check for folder art only if we haven't found one in this dir yet.
		name := strings.ToLower(d.Name())
		for _, artName := range folderArtNames {
			if name == artName {
				dir := filepath.Dir(path)
				if _, seen := folderArt[dir]; !seen {
					folderArt[dir] = path
				}
				return nil
			}
		}

		return nil
	})
	close(jobs) // Signal workers that walking is done.
	writerWg.Wait()

	if walkErr != nil {
		return fmt.Errorf("walking music dir: %w", walkErr)
	}
	if writerErr != nil {
		return writerErr
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

func readTrack(path, ext, contentType string, log *slog.Logger) (info trackInfo, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic reading tags: %v", r)
		}
	}()
	f, err := os.Open(path)
	if err != nil {
		return trackInfo{}, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return trackInfo{}, err
	}

	info = trackInfo{
		path:        path,
		ext:         strings.TrimPrefix(ext, "."),
		contentType: contentType,
		size:        stat.Size(),
	}

	var audioOffset int64

	// Use lightweight ID3v2 reader for MP3 — seeks past APIC frames
	// without reading their data from disk at all.
	if ext == ".mp3" {
		if tags, ok := readID3v2(f); ok {
			info.title = tags.title
			info.artist = tags.artist
			info.album = tags.album
			info.genre = tags.genre
			info.year = tags.year
			info.track = tags.track
			info.disc = tags.disc
			audioOffset = tags.tagSize
		}
	} else {
		// Fallback to dhowden/tag for FLAC, OGG, M4A, etc.
		meta, tagErr := tag.ReadFrom(f)
		if tagErr == nil {
			info.title = meta.Title()
			info.artist = meta.Artist()
			info.album = meta.Album()
			info.genre = meta.Genre()
			info.year = meta.Year()
			t, _ := meta.Track()
			info.track = t
			d, _ := meta.Disc()
			info.disc = d
		}
	}

	// Fill in defaults from filename/path.
	if info.title == "" {
		info.title = strings.TrimSuffix(filepath.Base(path), ext)
	}
	if info.artist == "" {
		info.artist = "Unknown Artist"
	}
	if info.album == "" {
		info.album = filepath.Base(filepath.Dir(path))
	}

	// Parse duration from the same open file handle.
	// For MP3, audioOffset lets us skip re-reading the ID3 header.
	dur, bitrate := parseDuration(f, stat.Size(), audioOffset, ext, log)
	if dur > 0 {
		info.duration = dur
		info.bitrate = bitrate
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
	id := db.NewID()
	var resultID string
	err := sqlitex.ExecuteTransient(conn, `
		INSERT INTO artist (id, name) VALUES (?, ?)
		ON CONFLICT(name) DO UPDATE SET name = name
		RETURNING id
	`, &sqlitex.ExecOptions{
		Args: []any{id, name},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			resultID = stmt.ColumnText(0)
			return nil
		},
	})
	return resultID, err
}

func (s *Scanner) upsertAlbum(conn *sqlite.Conn, artistID, name string, year int, genre string) (string, error) {
	id := db.NewID()
	var resultID string
	err := sqlitex.ExecuteTransient(conn, `
		INSERT INTO album (id, artist_id, name, year, genre) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(artist_id, name) DO UPDATE SET year = excluded.year, genre = excluded.genre
		RETURNING id
	`, &sqlitex.ExecOptions{
		Args: []any{id, artistID, name, year, genre},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			resultID = stmt.ColumnText(0)
			return nil
		},
	})
	return resultID, err
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
