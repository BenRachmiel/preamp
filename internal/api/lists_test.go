package api

import "testing"

func TestGetAlbumList2Newest(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=newest")

	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 2 {
		t.Errorf("expected 2 albums, got %d", len(albums))
	}
}

func TestGetAlbumList2AlphabeticalByName(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=alphabeticalByName")

	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) < 2 {
		t.Fatalf("expected 2 albums, got %d", len(albums))
	}
	// Blue Album should come before Gold alphabetically.
	first := albums[0].(map[string]any)
	if first["name"] != "Blue Album" {
		t.Errorf("first album = %v, want Blue Album", first["name"])
	}
}

func TestGetAlbumList2AlphabeticalByArtist(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=alphabeticalByArtist")

	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) < 2 {
		t.Fatalf("expected 2 albums, got %d", len(albums))
	}
	first := albums[0].(map[string]any)
	if first["artist"] != "ABBA" {
		t.Errorf("first artist = %v, want ABBA (alphabetical)", first["artist"])
	}
}

func TestGetAlbumList2ByGenre(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=byGenre&genre=Pop")

	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 1 {
		t.Errorf("expected 1 Pop album, got %d", len(albums))
	}
}

func TestGetAlbumList2ByYear(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=byYear&fromYear=1990&toYear=1993")

	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 1 {
		t.Fatalf("expected 1 album (Gold 1992), got %d", len(albums))
	}
	a := albums[0].(map[string]any)
	if a["name"] != "Gold" {
		t.Errorf("album name = %v, want Gold", a["name"])
	}
}

func TestGetAlbumList2ByYearFullRange(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=byYear&fromYear=1990&toYear=2000")

	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 2 {
		t.Errorf("expected 2 albums, got %d", len(albums))
	}
}

func TestGetAlbumList2Random(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=random&size=1")

	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 1 {
		t.Errorf("expected 1 random album, got %d", len(albums))
	}
}

func TestGetAlbumList2Recent(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=recent&u=testuser")

	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 0 {
		t.Errorf("expected 0 albums for recent with no plays, got %d", len(albums))
	}
}

func TestGetAlbumList2Frequent(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=frequent&u=testuser")

	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 0 {
		t.Errorf("expected 0 albums for frequent with no plays, got %d", len(albums))
	}
}

func TestGetAlbumList2MissingType(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getAlbumList2?")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status for missing type")
	}
}

func TestGetAlbumList2UnknownType(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=bogus")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status for unknown type")
	}
}

func TestGetAlbumList2EmptyResult(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getAlbumList2?type=byGenre&genre=Metal")

	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 0 {
		t.Errorf("expected 0 albums for Metal, got %d", len(albums))
	}
}

func TestGetAlbumList2Starred(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	// Star one album.
	getJSON(t, srv, "/rest/star?u=testuser&albumId=alb1")

	resp := getJSON(t, srv, "/rest/getAlbumList2?type=starred&u=testuser")
	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 1 {
		t.Fatalf("expected 1 starred album, got %d", len(albums))
	}
	a := albums[0].(map[string]any)
	if a["name"] != "Gold" {
		t.Errorf("starred album name = %v, want Gold", a["name"])
	}
}

func TestGetAlbumList2StarredEmpty(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	resp := getJSON(t, srv, "/rest/getAlbumList2?type=starred&u=testuser")
	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 0 {
		t.Errorf("expected 0 starred albums, got %d", len(albums))
	}
}

func TestGetAlbumList2RecentEmpty(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	resp := getJSON(t, srv, "/rest/getAlbumList2?type=recent&u=testuser")
	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 0 {
		t.Errorf("expected 0 recent albums, got %d", len(albums))
	}
}

func TestGetAlbumList2FrequentEmpty(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	resp := getJSON(t, srv, "/rest/getAlbumList2?type=frequent&u=testuser")
	al := resp["albumList2"].(map[string]any)
	albums := al["album"].([]any)
	if len(albums) != 0 {
		t.Errorf("expected 0 frequent albums, got %d", len(albums))
	}
}

func TestGetRandomSongs(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getRandomSongs?size=2")

	rs := resp["randomSongs"].(map[string]any)
	songs := rs["song"].([]any)
	if len(songs) != 2 {
		t.Errorf("expected 2 random songs, got %d", len(songs))
	}
}

func TestGetRandomSongsEmptyDB(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getRandomSongs?")

	rs := resp["randomSongs"].(map[string]any)
	songs := rs["song"].([]any)
	if len(songs) != 0 {
		t.Errorf("expected 0 songs on empty DB, got %d", len(songs))
	}
}

func TestGetStarred2Empty(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getStarred2?u=testuser")

	st := resp["starred2"].(map[string]any)
	if artists := st["artist"].([]any); len(artists) != 0 {
		t.Errorf("starred2.artist should be empty, got %d", len(artists))
	}
	if albums := st["album"].([]any); len(albums) != 0 {
		t.Errorf("starred2.album should be empty, got %d", len(albums))
	}
	if songs := st["song"].([]any); len(songs) != 0 {
		t.Errorf("starred2.song should be empty, got %d", len(songs))
	}
}

func TestGetSongsByGenre(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getSongsByGenre?genre=Pop")

	sg := resp["songsByGenre"].(map[string]any)
	songs := sg["song"].([]any)
	if len(songs) != 2 {
		t.Errorf("expected 2 Pop songs, got %d", len(songs))
	}
}

func TestGetSongsByGenreEmpty(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/getSongsByGenre?genre=Metal")

	sg := resp["songsByGenre"].(map[string]any)
	songs := sg["song"].([]any)
	if len(songs) != 0 {
		t.Errorf("expected 0 songs for Metal, got %d", len(songs))
	}
}

func TestGetSongsByGenreMissingGenre(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getSongsByGenre?")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}
