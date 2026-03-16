package api

import "testing"

func TestCreatePlaylist(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	resp := getJSON(t, srv, "/rest/createPlaylist?u=testuser&name=MyPlaylist")
	if resp["status"] != "ok" {
		t.Fatalf("status = %v, want ok", resp["status"])
	}

	// Verify getPlaylists returns it.
	plResp := getJSON(t, srv, "/rest/getPlaylists?u=testuser")
	pls := plResp["playlists"].(map[string]any)
	plList := pls["playlist"].([]any)
	if len(plList) != 1 {
		t.Fatalf("expected 1 playlist, got %d", len(plList))
	}
	pl := plList[0].(map[string]any)
	if pl["name"] != "MyPlaylist" {
		t.Errorf("playlist name = %v, want MyPlaylist", pl["name"])
	}
}

func TestGetPlaylistWithSongs(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	// Create playlist with songs.
	createResp := getJSON(t, srv, "/rest/createPlaylist?u=testuser&name=WithSongs&songId=s1&songId=s3")
	if createResp["status"] != "ok" {
		t.Fatalf("create status = %v", createResp["status"])
	}

	// Get the playlist ID from the response.
	plData := createResp["playlist"].(map[string]any)
	plID := plData["id"].(string)

	// Fetch playlist with songs.
	getResp := getJSON(t, srv, "/rest/getPlaylist?u=testuser&id="+plID)
	pl := getResp["playlist"].(map[string]any)
	if pl["name"] != "WithSongs" {
		t.Errorf("name = %v, want WithSongs", pl["name"])
	}
	songs := pl["entry"].([]any)
	if len(songs) != 2 {
		t.Fatalf("expected 2 songs, got %d", len(songs))
	}
	// Verify order.
	if songs[0].(map[string]any)["title"] != "Dancing Queen" {
		t.Errorf("first song = %v, want Dancing Queen", songs[0].(map[string]any)["title"])
	}
	if songs[1].(map[string]any)["title"] != "Buddy Holly" {
		t.Errorf("second song = %v, want Buddy Holly", songs[1].(map[string]any)["title"])
	}
}

func TestUpdatePlaylistRename(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	createResp := getJSON(t, srv, "/rest/createPlaylist?u=testuser&name=OldName")
	plData := createResp["playlist"].(map[string]any)
	plID := plData["id"].(string)

	resp := getJSON(t, srv, "/rest/updatePlaylist?u=testuser&playlistId="+plID+"&name=NewName")
	if resp["status"] != "ok" {
		t.Fatalf("update status = %v", resp["status"])
	}

	getResp := getJSON(t, srv, "/rest/getPlaylist?u=testuser&id="+plID)
	pl := getResp["playlist"].(map[string]any)
	if pl["name"] != "NewName" {
		t.Errorf("name = %v, want NewName", pl["name"])
	}
}

func TestUpdatePlaylistAddRemoveSongs(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	// Create with s1.
	createResp := getJSON(t, srv, "/rest/createPlaylist?u=testuser&name=EditMe&songId=s1&songId=s2")
	plData := createResp["playlist"].(map[string]any)
	plID := plData["id"].(string)

	// Remove first song (index 0), add s3.
	resp := getJSON(t, srv, "/rest/updatePlaylist?u=testuser&playlistId="+plID+"&songIndexToRemove=0&songIdToAdd=s3")
	if resp["status"] != "ok" {
		t.Fatalf("update status = %v", resp["status"])
	}

	getResp := getJSON(t, srv, "/rest/getPlaylist?u=testuser&id="+plID)
	pl := getResp["playlist"].(map[string]any)
	songs := pl["entry"].([]any)
	if len(songs) != 2 {
		t.Fatalf("expected 2 songs (s2 + s3), got %d", len(songs))
	}
	if songs[0].(map[string]any)["title"] != "Waterloo" {
		t.Errorf("first song = %v, want Waterloo", songs[0].(map[string]any)["title"])
	}
	if songs[1].(map[string]any)["title"] != "Buddy Holly" {
		t.Errorf("second song = %v, want Buddy Holly", songs[1].(map[string]any)["title"])
	}
}

func TestDeletePlaylist(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	createResp := getJSON(t, srv, "/rest/createPlaylist?u=testuser&name=ToDelete")
	plData := createResp["playlist"].(map[string]any)
	plID := plData["id"].(string)

	resp := getJSON(t, srv, "/rest/deletePlaylist?u=testuser&id="+plID)
	if resp["status"] != "ok" {
		t.Fatalf("delete status = %v", resp["status"])
	}

	// Verify getPlaylists is empty.
	plResp := getJSON(t, srv, "/rest/getPlaylists?u=testuser")
	pls := plResp["playlists"].(map[string]any)
	plList := pls["playlist"].([]any)
	if len(plList) != 0 {
		t.Errorf("expected 0 playlists after delete, got %d", len(plList))
	}
}

func TestCreatePlaylistMissingName(t *testing.T) {
	srv := testServer(t)

	resp := getJSON(t, srv, "/rest/createPlaylist?u=testuser")
	if resp["status"] != "failed" {
		t.Errorf("expected failed status for missing name")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestPlaylistPerUser(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	// User A creates a playlist.
	getJSON(t, srv, "/rest/createPlaylist?u=alice&name=AlicePlaylist")

	// User B should see no playlists.
	plResp := getJSON(t, srv, "/rest/getPlaylists?u=bob")
	pls := plResp["playlists"].(map[string]any)
	plList := pls["playlist"].([]any)
	if len(plList) != 0 {
		t.Errorf("expected 0 playlists for bob, got %d", len(plList))
	}

	// User A should see 1.
	plResp = getJSON(t, srv, "/rest/getPlaylists?u=alice")
	pls = plResp["playlists"].(map[string]any)
	plList = pls["playlist"].([]any)
	if len(plList) != 1 {
		t.Errorf("expected 1 playlist for alice, got %d", len(plList))
	}
}

func TestCreatePlaylistOverwrite(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	// Create playlist with s1.
	createResp := getJSON(t, srv, "/rest/createPlaylist?u=testuser&name=Overwrite&songId=s1")
	plData := createResp["playlist"].(map[string]any)
	plID := plData["id"].(string)

	// Overwrite with s3 using playlistId.
	resp := getJSON(t, srv, "/rest/createPlaylist?u=testuser&playlistId="+plID+"&songId=s3")
	if resp["status"] != "ok" {
		t.Fatalf("overwrite status = %v", resp["status"])
	}

	// Verify songs replaced.
	getResp := getJSON(t, srv, "/rest/getPlaylist?u=testuser&id="+plID)
	pl := getResp["playlist"].(map[string]any)
	songs := pl["entry"].([]any)
	if len(songs) != 1 {
		t.Fatalf("expected 1 song after overwrite, got %d", len(songs))
	}
	if songs[0].(map[string]any)["title"] != "Buddy Holly" {
		t.Errorf("song = %v, want Buddy Holly", songs[0].(map[string]any)["title"])
	}
}
