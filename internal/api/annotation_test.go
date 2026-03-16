package api

import "testing"

// --- Star/unstar tests ---

func TestStarSong(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	resp := getJSON(t, srv, "/rest/star?u=testuser&id=s1")
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}

	// Verify it shows up in getStarred2.
	starred := getJSON(t, srv, "/rest/getStarred2?u=testuser")
	st := starred["starred2"].(map[string]any)
	songs := st["song"].([]any)
	if len(songs) != 1 {
		t.Fatalf("expected 1 starred song, got %d", len(songs))
	}
	s := songs[0].(map[string]any)
	if s["title"] != "Dancing Queen" {
		t.Errorf("starred song title = %v, want Dancing Queen", s["title"])
	}
}

func TestStarAlbum(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	resp := getJSON(t, srv, "/rest/star?u=testuser&albumId=alb1")
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}

	starred := getJSON(t, srv, "/rest/getStarred2?u=testuser")
	st := starred["starred2"].(map[string]any)
	albums := st["album"].([]any)
	if len(albums) != 1 {
		t.Fatalf("expected 1 starred album, got %d", len(albums))
	}
	a := albums[0].(map[string]any)
	if a["name"] != "Gold" {
		t.Errorf("starred album name = %v, want Gold", a["name"])
	}
}

func TestStarArtist(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	resp := getJSON(t, srv, "/rest/star?u=testuser&artistId=art1")
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}

	starred := getJSON(t, srv, "/rest/getStarred2?u=testuser")
	st := starred["starred2"].(map[string]any)
	artists := st["artist"].([]any)
	if len(artists) != 1 {
		t.Fatalf("expected 1 starred artist, got %d", len(artists))
	}
	a := artists[0].(map[string]any)
	if a["name"] != "ABBA" {
		t.Errorf("starred artist name = %v, want ABBA", a["name"])
	}
}

func TestStarMissingParams(t *testing.T) {
	srv := testServer(t)

	resp := getJSON(t, srv, "/rest/star?u=testuser")
	if resp["status"] != "failed" {
		t.Errorf("expected failed status for missing star params")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestStarIdempotent(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	// Star the same song twice — should not error or create duplicates.
	getJSON(t, srv, "/rest/star?u=testuser&id=s1")
	resp := getJSON(t, srv, "/rest/star?u=testuser&id=s1")
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok on duplicate star", resp["status"])
	}

	starred := getJSON(t, srv, "/rest/getStarred2?u=testuser")
	st := starred["starred2"].(map[string]any)
	songs := st["song"].([]any)
	if len(songs) != 1 {
		t.Errorf("expected 1 starred song after duplicate star, got %d", len(songs))
	}
}

func TestUnstarSong(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	// Star then unstar.
	getJSON(t, srv, "/rest/star?u=testuser&id=s1")
	resp := getJSON(t, srv, "/rest/unstar?u=testuser&id=s1")
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}

	// Verify it's gone from getStarred2.
	starred := getJSON(t, srv, "/rest/getStarred2?u=testuser")
	st := starred["starred2"].(map[string]any)
	songs := st["song"].([]any)
	if len(songs) != 0 {
		t.Errorf("expected 0 starred songs after unstar, got %d", len(songs))
	}
}

func TestUnstarMissingParams(t *testing.T) {
	srv := testServer(t)

	resp := getJSON(t, srv, "/rest/unstar?u=testuser")
	if resp["status"] != "failed" {
		t.Errorf("expected failed status for missing unstar params")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestUnstarNonexistent(t *testing.T) {
	srv := testServer(t)

	// Unstarring something that was never starred should succeed silently.
	resp := getJSON(t, srv, "/rest/unstar?u=testuser&id=nonexistent")
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok for unstarring nonexistent item", resp["status"])
	}
}

func TestStarredPerUser(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	// User A stars a song.
	getJSON(t, srv, "/rest/star?u=alice&id=s1")

	// User B should see no starred songs.
	starred := getJSON(t, srv, "/rest/getStarred2?u=bob")
	st := starred["starred2"].(map[string]any)
	songs := st["song"].([]any)
	if len(songs) != 0 {
		t.Errorf("expected 0 starred songs for bob, got %d", len(songs))
	}

	// User A should see 1.
	starred = getJSON(t, srv, "/rest/getStarred2?u=alice")
	st = starred["starred2"].(map[string]any)
	songs = st["song"].([]any)
	if len(songs) != 1 {
		t.Errorf("expected 1 starred song for alice, got %d", len(songs))
	}
}

func TestGetStarred2EmptyNoStars(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	resp := getJSON(t, srv, "/rest/getStarred2?u=testuser")
	st := resp["starred2"].(map[string]any)
	if artists := st["artist"].([]any); len(artists) != 0 {
		t.Errorf("expected 0 starred artists, got %d", len(artists))
	}
	if albums := st["album"].([]any); len(albums) != 0 {
		t.Errorf("expected 0 starred albums, got %d", len(albums))
	}
	if songs := st["song"].([]any); len(songs) != 0 {
		t.Errorf("expected 0 starred songs, got %d", len(songs))
	}
}

func TestStarMultipleItems(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	// Star two songs at once.
	resp := getJSON(t, srv, "/rest/star?u=testuser&id=s1&id=s2")
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}

	starred := getJSON(t, srv, "/rest/getStarred2?u=testuser")
	st := starred["starred2"].(map[string]any)
	songs := st["song"].([]any)
	if len(songs) != 2 {
		t.Errorf("expected 2 starred songs, got %d", len(songs))
	}
}

// --- Scrobble tests ---

func TestScrobbleSong(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	resp := getJSON(t, srv, "/rest/scrobble?u=testuser&id=s1")
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}

	// Verify getAlbumList2 recent returns it.
	recent := getJSON(t, srv, "/rest/getAlbumList2?type=recent&u=testuser")
	al := recent["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 1 {
		t.Fatalf("expected 1 recent album, got %d", len(albums))
	}
	a := albums[0].(map[string]any)
	if a["name"] != "Gold" {
		t.Errorf("recent album name = %v, want Gold", a["name"])
	}
}

func TestScrobbleMultiple(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	// Scrobble s1 three times, s3 once.
	getJSON(t, srv, "/rest/scrobble?u=testuser&id=s1")
	getJSON(t, srv, "/rest/scrobble?u=testuser&id=s1")
	getJSON(t, srv, "/rest/scrobble?u=testuser&id=s1")
	getJSON(t, srv, "/rest/scrobble?u=testuser&id=s3")

	// Frequent should rank Gold (3 plays) above Blue Album (1 play).
	freq := getJSON(t, srv, "/rest/getAlbumList2?type=frequent&u=testuser")
	al := freq["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 2 {
		t.Fatalf("expected 2 frequent albums, got %d", len(albums))
	}
	first := albums[0].(map[string]any)
	if first["name"] != "Gold" {
		t.Errorf("most frequent album = %v, want Gold", first["name"])
	}
}

func TestScrobbleMissingId(t *testing.T) {
	srv := testServer(t)

	resp := getJSON(t, srv, "/rest/scrobble?u=testuser")
	if resp["status"] != "failed" {
		t.Errorf("expected failed status for missing id")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestScrobbleMissingUser(t *testing.T) {
	srv := testServer(t)

	resp := getJSON(t, srv, "/rest/scrobble?id=s1")
	if resp["status"] != "failed" {
		t.Errorf("expected failed status for missing user")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestScrobblePerUser(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	// User A scrobbles.
	getJSON(t, srv, "/rest/scrobble?u=alice&id=s1")

	// User B should see no recent albums.
	recent := getJSON(t, srv, "/rest/getAlbumList2?type=recent&u=bob")
	al := recent["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 0 {
		t.Errorf("expected 0 recent albums for bob, got %d", len(albums))
	}
}

func TestScrobbleBatch(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	// Batch scrobble multiple IDs at once (Symfonium does this).
	resp := getJSON(t, srv, "/rest/scrobble?u=testuser&id=s1&id=s3")
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}

	recent := getJSON(t, srv, "/rest/getAlbumList2?type=recent&u=testuser")
	al := recent["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 2 {
		t.Errorf("expected 2 recent albums after batch scrobble, got %d", len(albums))
	}
}

// --- setRating tests ---

func TestSetRating(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	resp := getJSON(t, srv, "/rest/setRating?u=testuser&id=s1&rating=5")
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}

	// Remove rating with 0.
	resp = getJSON(t, srv, "/rest/setRating?u=testuser&id=s1&rating=0")
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok for rating=0", resp["status"])
	}
}

func TestSetRatingInvalid(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	resp := getJSON(t, srv, "/rest/setRating?u=testuser&id=s1&rating=6")
	if resp["status"] != "failed" {
		t.Errorf("expected failed for rating=6")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestSetRatingMissingID(t *testing.T) {
	srv := testServer(t)

	resp := getJSON(t, srv, "/rest/setRating?u=testuser&rating=3")
	if resp["status"] != "failed" {
		t.Errorf("expected failed for missing id")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestSetRatingAppearsOnSong(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	getJSON(t, srv, "/rest/setRating?u=testuser&id=s1&rating=5")

	songResp := getJSON(t, srv, "/rest/getSong?u=testuser&id=s1")
	song := songResp["song"].(map[string]any)
	if song["userRating"].(float64) != 5 {
		t.Errorf("userRating = %v, want 5", song["userRating"])
	}
}

func TestSetRatingPerUser(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	getJSON(t, srv, "/rest/setRating?u=alice&id=s1&rating=5")

	// Bob should see no rating.
	songResp := getJSON(t, srv, "/rest/getSong?u=bob&id=s1")
	song := songResp["song"].(map[string]any)
	if _, has := song["userRating"]; has {
		t.Errorf("bob should not see alice's rating")
	}
}

func TestSetRatingInAlbum(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	getJSON(t, srv, "/rest/setRating?u=testuser&id=s1&rating=4")

	albumResp := getJSON(t, srv, "/rest/getAlbum?u=testuser&id=alb1")
	album := albumResp["album"].(map[string]any)
	songs := album["song"].([]any)
	for _, s := range songs {
		sm := s.(map[string]any)
		if sm["id"] == "s1" {
			if sm["userRating"].(float64) != 4 {
				t.Errorf("s1 userRating = %v, want 4", sm["userRating"])
			}
		} else {
			if _, has := sm["userRating"]; has {
				t.Errorf("non-rated song should not have userRating")
			}
		}
	}
}
