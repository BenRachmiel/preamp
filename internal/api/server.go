package api

import (
	"log/slog"
	"net/http"

	"github.com/BenRachmiel/preamp/internal/config"
	"github.com/BenRachmiel/preamp/internal/db"
	"github.com/BenRachmiel/preamp/internal/scanner"
)

type Server struct {
	cfg     *config.Config
	db      *db.DB
	log     *slog.Logger
	mux     *http.ServeMux
	scanner *scanner.Scanner
}

func NewServer(cfg *config.Config, database *db.DB, log *slog.Logger) *Server {
	s := &Server{
		cfg: cfg,
		db:  database,
		log: log,
		mux: http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.authMiddleware(s.mux)
}

func (s *Server) routes() {
	// All Subsonic endpoints live under /rest/
	// Using Go 1.22+ method routing.
	sub := func(pattern string, handler func(http.ResponseWriter, *http.Request)) {
		// Subsonic clients use both GET and POST.
		s.mux.HandleFunc("GET /rest/"+pattern, handler)
		s.mux.HandleFunc("POST /rest/"+pattern, handler)
		// Also handle with .view suffix (legacy).
		s.mux.HandleFunc("GET /rest/"+pattern+".view", handler)
		s.mux.HandleFunc("POST /rest/"+pattern+".view", handler)
	}

	// System
	sub("ping", s.handlePing)
	sub("getLicense", s.handleGetLicense)
	sub("getOpenSubsonicExtensions", s.handleGetOpenSubsonicExtensions)

	// Browsing
	sub("getMusicFolders", s.handleGetMusicFolders)
	sub("getArtists", s.handleGetArtists)
	sub("getArtist", s.handleGetArtist)
	sub("getAlbum", s.handleGetAlbum)
	sub("getSong", s.handleGetSong)
	sub("getGenres", s.handleGetGenres)

	// Search
	sub("search3", s.handleSearch3)

	// Media
	sub("stream", s.handleStream)
	sub("download", s.handleDownload)
	sub("getCoverArt", s.handleGetCoverArt)

	// Lists
	sub("getAlbumList2", s.handleGetAlbumList2)
	sub("getRandomSongs", s.handleGetRandomSongs)
	sub("getStarred2", s.handleGetStarred2)
	sub("getSongsByGenre", s.handleGetSongsByGenre)

	// Scanning
	sub("startScan", s.handleStartScan)
	sub("getScanStatus", s.handleGetScanStatus)
}
