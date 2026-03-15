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
	resp.OpenSubsonicExt = []OpenSubsonicExt{}
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
