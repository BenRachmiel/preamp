package api

import "testing"

func TestSearch3FTS(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/search3?query=dancing")

	sr := resp["searchResult3"].(map[string]any)
	songs := sr["song"].([]any)
	if len(songs) != 1 {
		t.Fatalf("expected 1 song for 'dancing', got %d", len(songs))
	}
	s := songs[0].(map[string]any)
	if s["title"] != "Dancing Queen" {
		t.Errorf("title = %v", s["title"])
	}
}

func TestSearch3EmptyResult(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/search3?query=zzzznothing")

	sr := resp["searchResult3"].(map[string]any)
	// All arrays should be [] not null.
	artists := sr["artist"].([]any)
	albums := sr["album"].([]any)
	songs := sr["song"].([]any)
	if len(artists) != 0 {
		t.Errorf("artist should be empty, got %d", len(artists))
	}
	if len(albums) != 0 {
		t.Errorf("album should be empty, got %d", len(albums))
	}
	if len(songs) != 0 {
		t.Errorf("song should be empty, got %d", len(songs))
	}
}

func TestSearch3ByArtist(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/search3?query=ABBA")

	sr := resp["searchResult3"].(map[string]any)
	artists := sr["artist"].([]any)
	if len(artists) != 1 {
		t.Errorf("expected 1 artist for 'ABBA', got %d", len(artists))
	}
}

func TestSearch3EmptyQueryReturnsAll(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/search3?query=")

	if resp["status"] != "ok" {
		t.Fatalf("expected ok status for empty query, got %v", resp["status"])
	}
	sr := resp["searchResult3"].(map[string]any)
	artists := sr["artist"].([]any)
	if len(artists) < 2 {
		t.Errorf("expected at least 2 artists for empty query, got %d", len(artists))
	}
}

func TestSearch3Offset(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	// Get all artists, then verify offset skips some.
	all := getJSON(t, srv, "/rest/search3?query=&artistCount=100")
	allSR := all["searchResult3"].(map[string]any)
	total := len(allSR["artist"].([]any))

	offset := getJSON(t, srv, "/rest/search3?query=&artistCount=100&artistOffset=1")
	offSR := offset["searchResult3"].(map[string]any)
	got := len(offSR["artist"].([]any))

	if got != total-1 {
		t.Errorf("expected %d artists with offset=1, got %d", total-1, got)
	}
}

func TestSearch3AlbumResults(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)
	resp := getJSON(t, srv, "/rest/search3?query=Gold")

	sr := resp["searchResult3"].(map[string]any)
	albums := sr["album"].([]any)
	if len(albums) != 1 {
		t.Fatalf("expected 1 album for 'Gold', got %d", len(albums))
	}
	a := albums[0].(map[string]any)
	if a["name"] != "Gold" {
		t.Errorf("album name = %v, want Gold", a["name"])
	}
}
