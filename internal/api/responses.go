package api

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
)

const (
	APIVersion = "1.16.1"
	ServerName = "preamp"
)

// SubsonicResponse is the top-level envelope for all Subsonic API responses.
type SubsonicResponse struct {
	XMLName xml.Name `xml:"subsonic-response" json:"-"`
	Status  string   `xml:"status,attr" json:"status"`
	Version string   `xml:"version,attr" json:"version"`
	Type    string   `xml:"type,attr" json:"type"`

	// Embedded response bodies — only one should be set per response.
	Error              *APIError              `xml:"error,omitempty" json:"error,omitempty"`
	License            *License               `xml:"license,omitempty" json:"license,omitempty"`
	MusicFolders       *MusicFolders          `xml:"musicFolders,omitempty" json:"musicFolders,omitempty"`
	OpenSubsonicExt    []OpenSubsonicExt      `xml:"openSubsonicExtensions,omitempty" json:"openSubsonicExtensions,omitempty"`
	Artists            *ArtistsID3            `xml:"artists,omitempty" json:"artists,omitempty"`
	Artist             *ArtistWithAlbumsID3   `xml:"artist,omitempty" json:"artist,omitempty"`
	Album              *AlbumWithSongsID3     `xml:"album,omitempty" json:"album,omitempty"`
	Song               *SongID3               `xml:"song,omitempty" json:"song,omitempty"`
	Genres             *Genres                `xml:"genres,omitempty" json:"genres,omitempty"`
	SearchResult3      *SearchResult3         `xml:"searchResult3,omitempty" json:"searchResult3,omitempty"`
	AlbumList2         *AlbumList2            `xml:"albumList2,omitempty" json:"albumList2,omitempty"`
	RandomSongs        *RandomSongs           `xml:"randomSongs,omitempty" json:"randomSongs,omitempty"`
	Starred2           *Starred2              `xml:"starred2,omitempty" json:"starred2,omitempty"`
	SongsByGenre       *SongsByGenre          `xml:"songsByGenre,omitempty" json:"songsByGenre,omitempty"`
	ScanStatus         *ScanStatus            `xml:"scanStatus,omitempty" json:"scanStatus,omitempty"`
	Playlists          *Playlists             `xml:"playlists,omitempty" json:"playlists,omitempty"`
	Playlist           *PlaylistWithSongs     `xml:"playlist,omitempty" json:"playlist,omitempty"`
	ArtistInfo2        *ArtistInfo2           `xml:"artistInfo2,omitempty" json:"artistInfo2,omitempty"`
	AlbumInfo          *AlbumInfo             `xml:"albumInfo,omitempty" json:"albumInfo,omitempty"`
	TopSongs           *TopSongs              `xml:"topSongs,omitempty" json:"topSongs,omitempty"`
	SimilarSongs2      *SimilarSongs2         `xml:"similarSongs2,omitempty" json:"similarSongs2,omitempty"`
}

type APIError struct {
	Code    int    `xml:"code,attr" json:"code"`
	Message string `xml:"message,attr" json:"message"`
}

type License struct {
	Valid bool `xml:"valid,attr" json:"valid"`
}

type MusicFolders struct {
	Folders []MusicFolder `xml:"musicFolder" json:"musicFolder"`
}

type MusicFolder struct {
	ID   int    `xml:"id,attr" json:"id"`
	Name string `xml:"name,attr,omitempty" json:"name,omitempty"`
}

type OpenSubsonicExt struct {
	Name     string `xml:"name,attr" json:"name"`
	Versions []int  `xml:"versions,attr" json:"versions"`
}

// --- Browsing types ---

type ArtistsID3 struct {
	Index []IndexID3 `xml:"index" json:"index"`
}

type IndexID3 struct {
	Name    string      `xml:"name,attr" json:"name"`
	Artists []ArtistID3 `xml:"artist" json:"artist"`
}

type ArtistID3 struct {
	ID         string `xml:"id,attr" json:"id"`
	Name       string `xml:"name,attr" json:"name"`
	AlbumCount int    `xml:"albumCount,attr" json:"albumCount"`
	CoverArt   string `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
}

type ArtistWithAlbumsID3 struct {
	ID         string     `xml:"id,attr" json:"id"`
	Name       string     `xml:"name,attr" json:"name"`
	AlbumCount int        `xml:"albumCount,attr" json:"albumCount"`
	Albums     []AlbumID3 `xml:"album" json:"album"`
}

type AlbumID3 struct {
	ID        string `xml:"id,attr" json:"id"`
	Name      string `xml:"name,attr" json:"name"`
	Artist    string `xml:"artist,attr,omitempty" json:"artist,omitempty"`
	ArtistID  string `xml:"artistId,attr,omitempty" json:"artistId,omitempty"`
	CoverArt  string `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
	SongCount int    `xml:"songCount,attr" json:"songCount"`
	Duration  int    `xml:"duration,attr" json:"duration"`
	Year      int    `xml:"year,attr,omitempty" json:"year,omitempty"`
	Genre     string `xml:"genre,attr,omitempty" json:"genre,omitempty"`
	Created   string `xml:"created,attr,omitempty" json:"created,omitempty"`
}

type AlbumWithSongsID3 struct {
	ID        string   `xml:"id,attr" json:"id"`
	Name      string   `xml:"name,attr" json:"name"`
	Artist    string   `xml:"artist,attr,omitempty" json:"artist,omitempty"`
	ArtistID  string   `xml:"artistId,attr,omitempty" json:"artistId,omitempty"`
	CoverArt  string   `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
	SongCount int      `xml:"songCount,attr" json:"songCount"`
	Duration  int      `xml:"duration,attr" json:"duration"`
	Year      int      `xml:"year,attr,omitempty" json:"year,omitempty"`
	Genre     string   `xml:"genre,attr,omitempty" json:"genre,omitempty"`
	Created   string   `xml:"created,attr,omitempty" json:"created,omitempty"`
	Songs     []SongID3 `xml:"song" json:"song"`
}

type SongID3 struct {
	ID          string `xml:"id,attr" json:"id"`
	Title       string `xml:"title,attr" json:"title"`
	Album       string `xml:"album,attr,omitempty" json:"album,omitempty"`
	Artist      string `xml:"artist,attr,omitempty" json:"artist,omitempty"`
	AlbumID     string `xml:"albumId,attr,omitempty" json:"albumId,omitempty"`
	ArtistID    string `xml:"artistId,attr,omitempty" json:"artistId,omitempty"`
	Track       int    `xml:"track,attr,omitempty" json:"track,omitempty"`
	Disc        int    `xml:"discNumber,attr,omitempty" json:"discNumber,omitempty"`
	Year        int    `xml:"year,attr,omitempty" json:"year,omitempty"`
	Genre       string `xml:"genre,attr,omitempty" json:"genre,omitempty"`
	Duration    int    `xml:"duration,attr,omitempty" json:"duration,omitempty"`
	Size        int64  `xml:"size,attr,omitempty" json:"size,omitempty"`
	Suffix      string `xml:"suffix,attr,omitempty" json:"suffix,omitempty"`
	BitRate     int    `xml:"bitRate,attr,omitempty" json:"bitRate,omitempty"`
	ContentType string `xml:"contentType,attr,omitempty" json:"contentType,omitempty"`
	Path        string `xml:"path,attr,omitempty" json:"path,omitempty"`
	CoverArt    string `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
	Type        string `xml:"type,attr,omitempty" json:"type,omitempty"`
	UserRating  int    `xml:"userRating,attr,omitempty" json:"userRating,omitempty"`
}

type Genres struct {
	Genres []Genre `xml:"genre" json:"genre"`
}

type Genre struct {
	Name       string `xml:",chardata" json:"value"`
	SongCount  int    `xml:"songCount,attr" json:"songCount"`
	AlbumCount int    `xml:"albumCount,attr" json:"albumCount"`
}

type SearchResult3 struct {
	Artists []ArtistID3 `xml:"artist" json:"artist"`
	Albums  []AlbumID3  `xml:"album" json:"album"`
	Songs   []SongID3   `xml:"song" json:"song"`
}

type AlbumList2 struct {
	Albums []AlbumID3 `xml:"album" json:"album"`
}

type RandomSongs struct {
	Songs []SongID3 `xml:"song" json:"song"`
}

type Starred2 struct {
	Artists []ArtistID3 `xml:"artist" json:"artist"`
	Albums  []AlbumID3  `xml:"album" json:"album"`
	Songs   []SongID3   `xml:"song" json:"song"`
}

type SongsByGenre struct {
	Songs []SongID3 `xml:"song" json:"song"`
}

type ScanStatus struct {
	Scanning bool `xml:"scanning,attr" json:"scanning"`
	Count    int  `xml:"count,attr,omitempty" json:"count,omitempty"`
}

// --- Playlist types ---

type PlaylistEntry struct {
	ID        string `xml:"id,attr" json:"id"`
	Name      string `xml:"name,attr" json:"name"`
	Comment   string `xml:"comment,attr" json:"comment"`
	SongCount int    `xml:"songCount,attr" json:"songCount"`
	Duration  int    `xml:"duration,attr" json:"duration"`
	Owner     string `xml:"owner,attr" json:"owner"`
	Public    bool   `xml:"public,attr" json:"public"`
	Created   string `xml:"created,attr" json:"created"`
	Changed   string `xml:"changed,attr" json:"changed"`
}

type Playlists struct {
	Playlists []PlaylistEntry `xml:"playlist" json:"playlist"`
}

type PlaylistWithSongs struct {
	PlaylistEntry
	Songs []SongID3 `xml:"entry" json:"entry"`
}

// --- Info types ---

type ArtistInfo2 struct {
	Biography      string      `xml:"biography,omitempty" json:"biography,omitempty"`
	MusicBrainzID  string      `xml:"musicBrainzId,omitempty" json:"musicBrainzId,omitempty"`
	LastFmURL      string      `xml:"lastFmUrl,omitempty" json:"lastFmUrl,omitempty"`
	SmallImageURL  string      `xml:"smallImageUrl,omitempty" json:"smallImageUrl,omitempty"`
	MediumImageURL string      `xml:"mediumImageUrl,omitempty" json:"mediumImageUrl,omitempty"`
	LargeImageURL  string      `xml:"largeImageUrl,omitempty" json:"largeImageUrl,omitempty"`
	SimilarArtist  []ArtistID3 `xml:"similarArtist" json:"similarArtist"`
}

type AlbumInfo struct {
	Notes          string `xml:"notes,omitempty" json:"notes,omitempty"`
	MusicBrainzID  string `xml:"musicBrainzId,omitempty" json:"musicBrainzId,omitempty"`
	LastFmURL      string `xml:"lastFmUrl,omitempty" json:"lastFmUrl,omitempty"`
	SmallImageURL  string `xml:"smallImageUrl,omitempty" json:"smallImageUrl,omitempty"`
	MediumImageURL string `xml:"mediumImageUrl,omitempty" json:"mediumImageUrl,omitempty"`
	LargeImageURL  string `xml:"largeImageUrl,omitempty" json:"largeImageUrl,omitempty"`
}

type TopSongs struct {
	Songs []SongID3 `xml:"song" json:"song"`
}

type SimilarSongs2 struct {
	Songs []SongID3 `xml:"song" json:"song"`
}

// --- Response helpers ---

func ok() SubsonicResponse {
	return SubsonicResponse{
		Status:  "ok",
		Version: APIVersion,
		Type:    ServerName,
	}
}

func errResponse(code int, msg string) SubsonicResponse {
	return SubsonicResponse{
		Status:  "failed",
		Version: APIVersion,
		Type:    ServerName,
		Error:   &APIError{Code: code, Message: msg},
	}
}

// jsonWrapper handles the non-standard {"subsonic-response": ...} JSON wrapping.
type jsonWrapper struct {
	Response SubsonicResponse `json:"subsonic-response"`
}

func writeResponse(w http.ResponseWriter, r *http.Request, resp SubsonicResponse) {
	format := r.FormValue("f")
	if format == "" {
		format = "xml"
	}

	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(jsonWrapper{Response: resp}); err != nil {
			// Client likely disconnected; log at debug level if we had a logger.
			// Nothing useful we can write back at this point.
			return
		}
	default:
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.Write([]byte(xml.Header))
		if err := xml.NewEncoder(w).Encode(resp); err != nil {
			return
		}
	}
}

func writeOK(w http.ResponseWriter, r *http.Request) {
	writeResponse(w, r, ok())
}

func writeError(w http.ResponseWriter, r *http.Request, code int, msg string) {
	writeResponse(w, r, errResponse(code, msg))
}
