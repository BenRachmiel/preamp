package api

import "testing"

func TestGetArtistInfo2(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	resp := getJSON(t, srv, "/rest/getArtistInfo2?u=testuser&id=art1")
	info := resp["artistInfo2"].(map[string]any)
	similar := info["similarArtist"].([]any)
	if len(similar) != 0 {
		t.Errorf("expected empty similarArtist, got %d", len(similar))
	}
}

func TestGetArtistInfo2MissingID(t *testing.T) {
	srv := testServer(t)

	resp := getJSON(t, srv, "/rest/getArtistInfo2?u=testuser")
	if resp["status"] != "failed" {
		t.Errorf("expected failed for missing id")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestGetAlbumInfo2(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	resp := getJSON(t, srv, "/rest/getAlbumInfo2?u=testuser&id=alb1")
	info, ok := resp["albumInfo"].(map[string]any)
	if !ok {
		t.Fatalf("missing albumInfo in response")
	}
	// Empty stub — no notes field expected.
	_ = info
}

func TestGetSimilarSongs2(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	resp := getJSON(t, srv, "/rest/getSimilarSongs2?u=testuser&id=s1")
	sim := resp["similarSongs2"].(map[string]any)
	songs := sim["song"].([]any)
	if len(songs) != 0 {
		t.Errorf("expected empty songs, got %d", len(songs))
	}
}

func TestGetTopSongs(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	// Scrobble s1 three times, s2 once.
	getJSON(t, srv, "/rest/scrobble?u=testuser&id=s1")
	getJSON(t, srv, "/rest/scrobble?u=testuser&id=s1")
	getJSON(t, srv, "/rest/scrobble?u=testuser&id=s1")
	getJSON(t, srv, "/rest/scrobble?u=testuser&id=s2")

	resp := getJSON(t, srv, "/rest/getTopSongs?u=testuser&artist=ABBA")
	top := resp["topSongs"].(map[string]any)
	songs := top["song"].([]any)
	if len(songs) != 2 {
		t.Fatalf("expected 2 top songs, got %d", len(songs))
	}
	// s1 (3 plays) should be first.
	if songs[0].(map[string]any)["title"] != "Dancing Queen" {
		t.Errorf("top song = %v, want Dancing Queen", songs[0].(map[string]any)["title"])
	}
}

func TestGetTopSongsEmpty(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	resp := getJSON(t, srv, "/rest/getTopSongs?u=testuser&artist=Unknown")
	top := resp["topSongs"].(map[string]any)
	songs := top["song"].([]any)
	if len(songs) != 0 {
		t.Errorf("expected 0 top songs for unknown artist, got %d", len(songs))
	}
}
