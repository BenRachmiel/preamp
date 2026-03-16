package api

import "testing"

func TestGetArtists(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getArtists?")

	artists, ok := resp["artists"].(map[string]any)
	if !ok {
		t.Fatalf("missing artists")
	}
	index, ok := artists["index"].([]any)
	if !ok {
		t.Fatalf("missing index, got: %v", artists)
	}
	if len(index) != 2 {
		t.Errorf("expected 2 index entries (A, W), got %d", len(index))
	}
}

func TestGetArtistsEmptyDB(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getArtists?")

	artists := resp["artists"].(map[string]any)
	index := artists["index"].([]any)
	if len(index) != 0 {
		t.Errorf("expected empty index, got %d entries", len(index))
	}
}

func TestGetArtist(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getArtist?id=art1")

	artist, ok := resp["artist"].(map[string]any)
	if !ok {
		t.Fatalf("missing artist")
	}
	if artist["name"] != "ABBA" {
		t.Errorf("name = %v", artist["name"])
	}
	albums := artist["album"].([]any)
	if len(albums) != 1 {
		t.Errorf("expected 1 album, got %d", len(albums))
	}
}

func TestGetArtistNotFound(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getArtist?id=nonexistent")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status for missing artist")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 70 {
		t.Errorf("error code = %v, want 70", apiErr["code"])
	}
}

func TestGetArtistMissingID(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getArtist?")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestGetAlbum(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbum?id=alb1")

	album, ok := resp["album"].(map[string]any)
	if !ok {
		t.Fatalf("missing album")
	}
	if album["name"] != "Gold" {
		t.Errorf("name = %v", album["name"])
	}
	songs := album["song"].([]any)
	if len(songs) != 2 {
		t.Errorf("expected 2 songs, got %d", len(songs))
	}
	// Verify song fields.
	s := songs[0].(map[string]any)
	if s["type"] != "music" {
		t.Errorf("song type = %v, want music", s["type"])
	}
}

func TestGetAlbumNotFound(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getAlbum?id=nonexistent")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 70 {
		t.Errorf("error code = %v, want 70", apiErr["code"])
	}
}

func TestGetAlbumMissingID(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getAlbum?")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestGetSong(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getSong?id=s1")

	song, ok := resp["song"].(map[string]any)
	if !ok {
		t.Fatalf("missing song")
	}
	if song["title"] != "Dancing Queen" {
		t.Errorf("title = %v", song["title"])
	}
	if song["artist"] != "ABBA" {
		t.Errorf("artist = %v", song["artist"])
	}
}

func TestGetSongNotFound(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getSong?id=nonexistent")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 70 {
		t.Errorf("error code = %v, want 70", apiErr["code"])
	}
}

func TestGetSongMissingID(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getSong?")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestCoverArtIDFormat(t *testing.T) {
	srv := testServer(t)
	seedDataWithFiles(t, srv)

	// Album response should return "al-{albumID}", not a filesystem path.
	resp := getJSON(t, srv, "/rest/getAlbum?id=alb1")
	album := resp["album"].(map[string]any)
	coverArt := album["coverArt"].(string)
	if coverArt != "al-alb1" {
		t.Errorf("album coverArt = %q, want %q", coverArt, "al-alb1")
	}

	// Song response should also use the al- prefix.
	songs := album["song"].([]any)
	songCover := songs[0].(map[string]any)["coverArt"].(string)
	if songCover != "al-alb1" {
		t.Errorf("song coverArt = %q, want %q", songCover, "al-alb1")
	}

	// Artist's album listing should also use al- prefix.
	resp = getJSON(t, srv, "/rest/getArtist?id=art1")
	artist := resp["artist"].(map[string]any)
	albums := artist["album"].([]any)
	artistAlbumCover := albums[0].(map[string]any)["coverArt"].(string)
	if artistAlbumCover != "al-alb1" {
		t.Errorf("artist album coverArt = %q, want %q", artistAlbumCover, "al-alb1")
	}
}

func TestCoverArtOmittedWhenNoCover(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv) // no cover_art set in DB

	// Album with no cover should omit coverArt field entirely.
	resp := getJSON(t, srv, "/rest/getAlbum?id=alb1")
	album := resp["album"].(map[string]any)
	if _, exists := album["coverArt"]; exists {
		t.Errorf("album coverArt should be omitted when no cover, got %v", album["coverArt"])
	}

	// Song with no cover should also omit coverArt.
	songs := album["song"].([]any)
	if _, exists := songs[0].(map[string]any)["coverArt"]; exists {
		t.Errorf("song coverArt should be omitted when no cover, got %v", songs[0].(map[string]any)["coverArt"])
	}
}

func TestGetGenres(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getGenres?")

	genres := resp["genres"].(map[string]any)
	genreList := genres["genre"].([]any)
	if len(genreList) != 2 {
		t.Errorf("expected 2 genres (Pop, Rock), got %d", len(genreList))
	}
}

func TestGetGenresEmpty(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getGenres?")

	genres := resp["genres"].(map[string]any)
	genreList := genres["genre"].([]any)
	if len(genreList) != 0 {
		t.Errorf("expected empty genres, got %d", len(genreList))
	}
}
