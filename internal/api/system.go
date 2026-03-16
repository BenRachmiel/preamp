package api

import "net/http"

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeOK(w, r)
}

func (s *Server) handleGetLicense(w http.ResponseWriter, r *http.Request) {
	resp := ok()
	resp.License = &License{Valid: true}
	writeResponse(w, r, resp)
}

func (s *Server) handleGetOpenSubsonicExtensions(w http.ResponseWriter, r *http.Request) {
	resp := ok()
	resp.OpenSubsonicExt = []OpenSubsonicExt{
		{Name: "apiKeyAuthentication", Versions: []int{1}},
	}
	writeResponse(w, r, resp)
}

func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	if username == "" {
		writeError(w, r, 10, "missing required param: username")
		return
	}
	resp := ok()
	resp.User = &User{
		Username:          username,
		ScrobblingEnabled: true,
		AdminRole:         true,
		SettingsRole:      true,
		DownloadRole:      true,
		UploadRole:        false,
		PlaylistRole:      true,
		CoverArtRole:      true,
		CommentRole:       false,
		PodcastRole:       false,
		StreamRole:        true,
		JukeboxRole:       false,
		ShareRole:         true,
		Folder:            []int{1},
	}
	writeResponse(w, r, resp)
}

func (s *Server) handleGetMusicFolders(w http.ResponseWriter, r *http.Request) {
	resp := ok()
	resp.MusicFolders = &MusicFolders{
		Folders: []MusicFolder{
			{ID: 1, Name: "Music"},
		},
	}
	writeResponse(w, r, resp)
}
