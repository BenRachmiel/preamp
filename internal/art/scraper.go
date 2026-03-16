package art

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/BenRachmiel/preamp/internal/db"
)

const (
	itunesSearchAPI = "https://itunes.apple.com/search"
	deezerSearchAPI = "https://api.deezer.com/search/album"
	userAgent       = "Preamp/0.1 (github.com/BenRachmiel/preamp)"
	artworkSize     = "600x600bb"
	minMatchScore   = 0.5
)

type Album struct {
	ID       string
	Name     string
	Artist   string
	SongPath string // path to a song in this album, to derive the album directory
}

// Scraper fetches missing album art from iTunes and Deezer.
type Scraper struct {
	database     *db.DB
	coverArtDir  string
	log          *slog.Logger
	client       *http.Client
	dryRun       bool
	saveToFolder bool
}

type Options struct {
	DryRun       bool
	SaveToFolder bool
}

func NewScraper(database *db.DB, coverArtDir string, log *slog.Logger, opts Options) *Scraper {
	return &Scraper{
		database:     database,
		coverArtDir:  coverArtDir,
		log:          log,
		client:       &http.Client{Timeout: 30 * time.Second},
		dryRun:       opts.DryRun,
		saveToFolder: opts.SaveToFolder,
	}
}

// Run finds albums missing cover art and fetches from iTunes (primary) or Deezer (fallback).
func (s *Scraper) Run() error {
	albums, err := s.albumsMissingArt()
	if err != nil {
		return fmt.Errorf("querying albums: %w", err)
	}

	s.log.Info("albums missing art", "count", len(albums))

	fetched := 0
	var failed []Album
	for _, album := range albums {
		if err := s.fetchArt(album); err != nil {
			s.log.Warn("fetching art", "album", album.Name, "artist", album.Artist, "err", err)
			failed = append(failed, album)
			continue
		}
		fetched++
	}

	if len(failed) > 0 {
		for _, a := range failed {
			s.log.Warn("missing artwork", "album", a.Name, "artist", a.Artist)
		}
	}
	s.log.Info("art scraping complete", "fetched", fetched, "failed", len(failed), "total", len(albums))
	return nil
}

func (s *Scraper) albumsMissingArt() ([]Album, error) {
	conn, put, err := s.database.ReadConn()
	if err != nil {
		return nil, err
	}
	defer put()

	var albums []Album
	err = sqlitex.ExecuteTransient(conn, `
		SELECT a.id, a.name, ar.name, MIN(s.path)
		FROM album a
		JOIN artist ar ON ar.id = a.artist_id
		JOIN song s ON s.album_id = a.id
		WHERE a.cover_art IS NULL OR a.cover_art = ''
		GROUP BY a.id
	`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			albums = append(albums, Album{
				ID:       stmt.ColumnText(0),
				Name:     stmt.ColumnText(1),
				Artist:   stmt.ColumnText(2),
				SongPath: stmt.ColumnText(3),
			})
			return nil
		},
	})
	return albums, err
}

func (s *Scraper) fetchArt(album Album) error {
	// Try iTunes first, then Deezer.
	artURL, source, err := s.findArtwork(album.Artist, album.Name)
	if err != nil {
		return err
	}
	if artURL == "" {
		return fmt.Errorf("no artwork found for %q by %q", album.Name, album.Artist)
	}

	s.log.Info("found artwork", "album", album.Name, "source", source, "url", artURL)

	if s.dryRun {
		return nil
	}

	imgData, err := s.downloadImage(artURL)
	if err != nil {
		return fmt.Errorf("downloading artwork: %w", err)
	}

	if s.saveToFolder {
		albumDir := filepath.Dir(album.SongPath)
		folderArtPath := filepath.Join(albumDir, "cover.jpg")
		if _, err := os.Stat(folderArtPath); err == nil {
			s.log.Info("folder art already exists, skipping", "path", folderArtPath)
		} else {
			f, err := os.OpenFile(folderArtPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err == nil {
				_, writeErr := f.Write(imgData)
				f.Close()
				if writeErr != nil {
					os.Remove(folderArtPath)
					return fmt.Errorf("writing folder art: %w", writeErr)
				}
				s.log.Info("saved folder art", "path", folderArtPath)
			}
		}
	}

	cachePath := filepath.Join(s.coverArtDir, album.ID+".jpg")
	if _, err := os.Stat(cachePath); err == nil {
		s.log.Info("cache art already exists, skipping", "path", cachePath)
	} else {
		f, err := os.OpenFile(cachePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_, writeErr := f.Write(imgData)
			f.Close()
			if writeErr != nil {
				os.Remove(cachePath)
				return fmt.Errorf("writing cache art: %w", writeErr)
			}
		}
	}

	conn, put, err := s.database.WriteConn()
	if err != nil {
		return err
	}
	defer put()

	return sqlitex.ExecuteTransient(conn, `UPDATE album SET cover_art = ? WHERE id = ?`, &sqlitex.ExecOptions{
		Args: []any{cachePath, album.ID},
	})
}

// findArtwork tries iTunes then Deezer, returns (url, source, error).
func (s *Scraper) findArtwork(artist, album string) (string, string, error) {
	artURL, err := s.searchItunes(artist, album)
	if err != nil {
		s.log.Debug("iTunes failed, trying Deezer", "err", err)
	}
	if artURL != "" {
		return artURL, "itunes", nil
	}

	artURL, err = s.searchDeezer(artist, album)
	if err != nil {
		return "", "", fmt.Errorf("all sources failed (last: Deezer: %w)", err)
	}
	if artURL != "" {
		return artURL, "deezer", nil
	}

	return "", "", nil
}

// --- iTunes ---

type itunesResult struct {
	ResultCount int `json:"resultCount"`
	Results     []struct {
		ArtistName    string `json:"artistName"`
		CollectionName string `json:"collectionName"`
		ArtworkURL100 string `json:"artworkUrl100"`
	} `json:"results"`
}

func (s *Scraper) searchItunes(artist, album string) (string, error) {
	term := artist + " " + album
	u := itunesSearchAPI + "?" + url.Values{
		"term":   {term},
		"entity": {"album"},
		"limit":  {"10"},
	}.Encode()

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("iTunes returned %d", resp.StatusCode)
	}

	var result itunesResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	// Score each result by artist+album similarity, pick the best.
	bestURL := ""
	bestScore := 0.0
	for _, r := range result.Results {
		score := matchScore(artist, album, r.ArtistName, r.CollectionName)
		if score > bestScore {
			bestScore = score
			bestURL = r.ArtworkURL100
		}
	}

	if bestScore < minMatchScore || bestURL == "" {
		return "", nil
	}

	return strings.Replace(bestURL, "100x100bb", artworkSize, 1), nil
}

// --- Deezer ---

type deezerResult struct {
	Data []struct {
		Title    string `json:"title"`
		CoverBig string `json:"cover_big"`
		Artist   struct {
			Name string `json:"name"`
		} `json:"artist"`
	} `json:"data"`
}

func (s *Scraper) searchDeezer(artist, album string) (string, error) {
	q := fmt.Sprintf(`artist:"%s" album:"%s"`, artist, album)
	u := deezerSearchAPI + "?" + url.Values{
		"q":     {q},
		"limit": {"10"},
	}.Encode()

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Deezer returned %d", resp.StatusCode)
	}

	var result deezerResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	bestURL := ""
	bestScore := 0.0
	for _, r := range result.Data {
		score := matchScore(artist, album, r.Artist.Name, r.Title)
		if score > bestScore {
			bestScore = score
			bestURL = r.CoverBig
		}
	}

	if bestScore < minMatchScore || bestURL == "" {
		return "", nil
	}

	return bestURL, nil
}

// --- shared ---

func (s *Scraper) downloadImage(artURL string) ([]byte, error) {
	req, err := http.NewRequest("GET", artURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artwork download returned %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}
	return data, nil
}

// matchScore returns 0.0–1.0 indicating how well a result matches the query.
// Artist match is weighted more heavily than album match.
func matchScore(wantArtist, wantAlbum, gotArtist, gotAlbum string) float64 {
	artistScore := stringSimilarity(wantArtist, gotArtist)
	albumScore := stringSimilarity(wantAlbum, gotAlbum)
	// Artist must match well — wrong artist is always wrong.
	if artistScore < 0.4 {
		return 0
	}
	return artistScore*0.6 + albumScore*0.4
}

// stringSimilarity compares two strings after normalization.
// Returns 1.0 for exact match, partial credit for containment.
func stringSimilarity(a, b string) float64 {
	a = normalize(a)
	b = normalize(b)
	if a == b {
		return 1.0
	}
	// One contains the other (e.g. "weezer" vs "weezer blue album").
	if strings.Contains(a, b) || strings.Contains(b, a) {
		shorter, longer := len(a), len(b)
		if shorter > longer {
			shorter, longer = longer, shorter
		}
		return float64(shorter) / float64(longer)
	}
	// Word overlap: fraction of query words present in the result.
	aWords := strings.Fields(a)
	bWords := strings.Fields(b)
	if len(aWords) == 0 {
		return 0
	}
	hits := 0
	for _, w := range aWords {
		for _, bw := range bWords {
			if w == bw {
				hits++
				break
			}
		}
	}
	return float64(hits) / float64(len(aWords))
}

// normalize lowercases and strips non-alphanumeric characters (except spaces).
func normalize(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			b.WriteRune(r)
		}
	}
	// Collapse multiple spaces.
	return strings.Join(strings.Fields(b.String()), " ")
}
