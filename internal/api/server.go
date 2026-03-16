package api

import (
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/BenRachmiel/preamp/internal/config"
	"github.com/BenRachmiel/preamp/internal/db"
	"github.com/BenRachmiel/preamp/internal/scanner"
)

type Server struct {
	cfg          *config.Config
	db           *db.DB
	log          *slog.Logger
	mux          *http.ServeMux // top-level mux dispatching /rest/ and /manage/
	subsonicMux  *http.ServeMux // Subsonic API routes
	scanner      *scanner.Scanner
	manageHandler http.Handler
}

func NewServer(cfg *config.Config, database *db.DB, log *slog.Logger) *Server {
	s := &Server{
		cfg:         cfg,
		db:          database,
		log:         log,
		mux:         http.NewServeMux(),
		subsonicMux: http.NewServeMux(),
	}
	s.subsonicRoutes()
	s.wireTopMux()
	return s
}

// SetManageHandler sets the management UI handler for /manage/ routes.
func (s *Server) SetManageHandler(h http.Handler) {
	s.manageHandler = h
	s.wireTopMux()
}

func (s *Server) wireTopMux() {
	s.mux = http.NewServeMux()
	s.mux.Handle("/rest/", s.authMiddleware(s.subsonicMux))
	if s.manageHandler != nil {
		s.mux.Handle("/manage/", s.manageHandler)
		s.mux.Handle("/manage", s.manageHandler)
	}
}

func (s *Server) Handler() http.Handler {
	return s.loggingMiddleware(s.mux)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func redactQuery(q url.Values) string {
	redacted := make(url.Values, len(q))
	for k, v := range q {
		if k == "p" || k == "apiKey" {
			redacted[k] = []string{"***"}
		} else {
			redacted[k] = v
		}
	}
	return redacted.Encode()
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"query", redactQuery(r.URL.Query()),
			"status", rec.status,
			"duration", time.Since(start),
		)
	})
}

func (s *Server) subsonicRoutes() {
	sub := func(pattern string, handler func(http.ResponseWriter, *http.Request)) {
		s.subsonicMux.HandleFunc("GET /rest/"+pattern, handler)
		s.subsonicMux.HandleFunc("POST /rest/"+pattern, handler)
		s.subsonicMux.HandleFunc("GET /rest/"+pattern+".view", handler)
		s.subsonicMux.HandleFunc("POST /rest/"+pattern+".view", handler)
	}

	// System
	sub("ping", s.handlePing)
	sub("getLicense", s.handleGetLicense)
	sub("getOpenSubsonicExtensions", s.handleGetOpenSubsonicExtensions)
	sub("getUser", s.handleGetUser)

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

	// Annotation
	sub("star", s.handleStar)
	sub("unstar", s.handleUnstar)
	sub("scrobble", s.handleScrobble)
	sub("setRating", s.handleSetRating)

	// Playlists
	sub("getPlaylists", s.handleGetPlaylists)
	sub("getPlaylist", s.handleGetPlaylist)
	sub("createPlaylist", s.handleCreatePlaylist)
	sub("updatePlaylist", s.handleUpdatePlaylist)
	sub("deletePlaylist", s.handleDeletePlaylist)

	// Info
	sub("getArtistInfo", s.handleGetArtistInfo2)
	sub("getArtistInfo2", s.handleGetArtistInfo2)
	sub("getAlbumInfo", s.handleGetAlbumInfo2)
	sub("getAlbumInfo2", s.handleGetAlbumInfo2)
	sub("getSimilarSongs", s.handleGetSimilarSongs2)
	sub("getSimilarSongs2", s.handleGetSimilarSongs2)
	sub("getTopSongs", s.handleGetTopSongs)

	// Scanning
	sub("startScan", s.handleStartScan)
	sub("getScanStatus", s.handleGetScanStatus)
}
