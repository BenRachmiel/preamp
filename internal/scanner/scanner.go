package scanner

import (
	"fmt"
	"io"
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
	db          *db.DB
	musicDir    string
	coverArtDir string
	log         *slog.Logger
	scanning    atomic.Bool
	count       atomic.Int64
}

func New(database *db.DB, musicDir, coverArtDir string, log *slog.Logger) *Scanner {
	return &Scanner{
		db:          database,
		musicDir:    musicDir,
		coverArtDir: coverArtDir,
		log:         log,
	}
}

func (s *Scanner) Scanning() bool {
	return s.scanning.Load()
}

func (s *Scanner) Count() int {
	return int(s.count.Load())
}

// Run performs a full scan of the music directory.
func (s *Scanner) Run() error {
	if !s.scanning.CompareAndSwap(false, true) {
		return fmt.Errorf("scan already in progress")
	}
	defer s.scanning.Store(false)
	s.count.Store(0)

	s.log.Info("starting scan", "dir", s.musicDir)

	// Collect all tracks first.
	var tracks []trackInfo
	err := filepath.WalkDir(s.musicDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			s.log.Warn("walk error", "path", path, "err", err)
			return nil // skip errors, keep scanning
		}
		if d.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		contentType, ok := supportedExts[ext]
		if !ok {
			return nil
		}

		info, err := readTrack(path, ext, contentType)
		if err != nil {
			s.log.Warn("reading track", "path", path, "err", err)
			return nil
		}

		// Parse accurate duration and bitrate.
		dur, br := parseDuration(path, ext, s.log)
		if dur > 0 {
			info.duration = dur
			info.bitrate = br
		}

		tracks = append(tracks, info)
		return nil
	})
	if err != nil {
		return fmt.Errorf("walking music dir: %w", err)
	}

	s.log.Info("found tracks", "count", len(tracks))

	// Batch insert into DB.
	conn, put, err := s.db.WriteConn()
	if err != nil {
		return fmt.Errorf("getting write conn: %w", err)
	}
	defer put()

	// Process in batches of 1000.
	const batchSize = 1000
	for i := 0; i < len(tracks); i += batchSize {
		end := i + batchSize
		if end > len(tracks) {
			end = len(tracks)
		}
		if err := s.insertBatch(conn, tracks[i:end]); err != nil {
			return fmt.Errorf("inserting batch: %w", err)
		}
		s.count.Store(int64(end))
	}

	// Pick up folder art for albums without embedded cover art.
	if err := s.findFolderArt(conn); err != nil {
		s.log.Warn("folder art detection", "err", err)
	}

	// Update album aggregates.
	if err := s.updateAlbumStats(conn); err != nil {
		return fmt.Errorf("updating album stats: %w", err)
	}

	// Rebuild FTS5 index.
	if err := s.rebuildFTS(conn); err != nil {
		return fmt.Errorf("rebuilding FTS index: %w", err)
	}

	s.log.Info("scan complete", "tracks", len(tracks))
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
	coverData   []byte
	coverExt    string // jpg, png
}

func readTrack(path, ext, contentType string) (trackInfo, error) {
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
		return info, nil
	}

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

	// Duration and bitrate are parsed separately via parseDuration()
	// after tag reading, since it needs the logger for ffprobe fallback.

	// Extract cover art.
	if pic := meta.Picture(); pic != nil && len(pic.Data) > 0 {
		info.coverData = pic.Data
		switch pic.MIMEType {
		case "image/png":
			info.coverExt = "png"
		default:
			info.coverExt = "jpg"
		}
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

		// Extract cover art (first track with art wins per album).
		if len(t.coverData) > 0 {
			coverPath := filepath.Join(s.coverArtDir, albumID+"."+t.coverExt)
			f, openErr := os.OpenFile(coverPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if openErr == nil {
				_, writeErr := f.Write(t.coverData)
				f.Close()
				if writeErr != nil {
					s.log.Warn("writing cover art", "album", t.album, "err", writeErr)
				} else {
					_ = sqlitex.ExecuteTransient(conn, `UPDATE album SET cover_art = ? WHERE id = ?`, &sqlitex.ExecOptions{
						Args: []any{coverPath, albumID},
					})
				}
			}
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
		UPDATE album SET
			song_count = (SELECT COUNT(*) FROM song WHERE song.album_id = album.id),
			duration = (SELECT COALESCE(SUM(duration), 0) FROM song WHERE song.album_id = album.id)
	`, nil)
}

// folderArtNames lists common cover art filenames to look for in album directories.
var folderArtNames = []string{
	"cover.jpg", "cover.png",
	"folder.jpg", "folder.png",
	"front.jpg", "front.png",
}

// findFolderArt checks album directories for common cover art files and copies
// them to the cover cache for albums that don't already have embedded cover art.
func (s *Scanner) findFolderArt(conn *sqlite.Conn) error {
	// Get albums without cover art, along with a sample song path to locate the directory.
	type albumDir struct {
		id  string
		dir string
	}
	var albums []albumDir

	err := sqlitex.ExecuteTransient(conn, `
		SELECT a.id, MIN(s.path) FROM album a
		JOIN song s ON s.album_id = a.id
		WHERE a.cover_art IS NULL OR a.cover_art = ''
		GROUP BY a.id
	`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			songPath := stmt.ColumnText(1)
			albums = append(albums, albumDir{
				id:  stmt.ColumnText(0),
				dir: filepath.Dir(songPath),
			})
			return nil
		},
	})
	if err != nil {
		return err
	}

	found := 0
	for _, album := range albums {
		for _, name := range folderArtNames {
			artPath := filepath.Join(album.dir, name)
			if _, err := os.Stat(artPath); err != nil {
				continue
			}

			// Copy to cover cache. O_EXCL ensures we never overwrite.
			dst := filepath.Join(s.coverArtDir, album.id+".jpg")
			out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				break // already exists or permission error
			}

			src, err := os.Open(artPath)
			if err != nil {
				out.Close()
				os.Remove(dst)
				s.log.Warn("opening folder art", "path", artPath, "err", err)
				break
			}
			_, copyErr := io.Copy(out, src)
			src.Close()
			out.Close()
			if copyErr != nil {
				os.Remove(dst)
				s.log.Warn("copying folder art", "album_id", album.id, "err", copyErr)
				break
			}

			if err := sqlitex.ExecuteTransient(conn, `UPDATE album SET cover_art = ? WHERE id = ?`, &sqlitex.ExecOptions{
				Args: []any{dst, album.id},
			}); err != nil {
				s.log.Warn("updating album cover_art", "album_id", album.id, "err", err)
			}
			found++
			break // found art for this album
		}
	}

	if found > 0 {
		s.log.Info("found folder art", "albums", found)
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
