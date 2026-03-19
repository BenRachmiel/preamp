package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testCollectorToken = "test-collector-secret-42"

// bearerGet sends a GET to the admin handler with a Bearer token.
func bearerGet(t *testing.T, srv *Server, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	srv.AdminHandler().ServeHTTP(w, req)
	return w
}

type playHistoryResponse struct {
	Entries []playHistoryEntry `json:"entries"`
}

func TestPlayHistoryTokenNotConfigured(t *testing.T) {
	srv := testServer(t) // no collectorToken
	w := bearerGet(t, srv, "/admin/playhistory", "anything")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestPlayHistoryWrongToken(t *testing.T) {
	srv := testServer(t, testServerOpts{collectorToken: testCollectorToken})
	w := bearerGet(t, srv, "/admin/playhistory", "wrong-token")

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestPlayHistoryNoTokenHeader(t *testing.T) {
	// Token configured but no Authorization header and no Remote-User → should
	// fall through to admin auth, which rejects (auth enabled, no Remote-User).
	srv := testServer(t, testServerOpts{
		collectorToken: testCollectorToken,
		encryptionKey:  testEncryptionKey,
	})
	w := bearerGet(t, srv, "/admin/playhistory", "")

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestPlayHistoryFallsThroughToAdminAuth(t *testing.T) {
	// No bearer token, but Remote-User set — should work via admin auth fallthrough.
	srv := testServer(t, testServerOpts{collectorToken: testCollectorToken})
	seedData(t, srv)

	w := adminGet(t, srv, "/admin/playhistory", "alice")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
	resp := decodeJSON[playHistoryResponse](t, w)
	if len(resp.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(resp.Entries))
	}
}

func TestPlayHistoryHappyPath(t *testing.T) {
	srv := testServer(t, testServerOpts{collectorToken: testCollectorToken})
	seedData(t, srv)

	// Scrobble two songs via the Subsonic endpoint.
	getJSON(t, srv, "/rest/scrobble?u=testuser&id=s1")
	getJSON(t, srv, "/rest/scrobble?u=testuser&id=s3")

	w := bearerGet(t, srv, "/admin/playhistory", testCollectorToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}

	resp := decodeJSON[playHistoryResponse](t, w)
	if len(resp.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(resp.Entries))
	}

	// First entry should be s1 (Dancing Queen).
	e := resp.Entries[0]
	if e.SongID != "s1" {
		t.Errorf("entries[0].songId = %q, want s1", e.SongID)
	}
	if e.Title != "Dancing Queen" {
		t.Errorf("entries[0].title = %q, want Dancing Queen", e.Title)
	}
	if e.Artist != "ABBA" {
		t.Errorf("entries[0].artist = %q, want ABBA", e.Artist)
	}
	if e.Album != "Gold" {
		t.Errorf("entries[0].album = %q, want Gold", e.Album)
	}
	if e.Rowid == 0 {
		t.Error("entries[0].rowid should be non-zero")
	}

	// Second entry should be s3 (Buddy Holly).
	if resp.Entries[1].SongID != "s3" {
		t.Errorf("entries[1].songId = %q, want s3", resp.Entries[1].SongID)
	}
	if resp.Entries[1].Artist != "Weezer" {
		t.Errorf("entries[1].artist = %q, want Weezer", resp.Entries[1].Artist)
	}
}

func TestPlayHistoryPagination(t *testing.T) {
	srv := testServer(t, testServerOpts{collectorToken: testCollectorToken})
	seedData(t, srv)

	// Scrobble 5 times.
	for range 5 {
		getJSON(t, srv, "/rest/scrobble?u=testuser&id=s1")
	}

	// Page 1: limit=2, since=0.
	w := bearerGet(t, srv, "/admin/playhistory?limit=2&since=0", testCollectorToken)
	if w.Code != http.StatusOK {
		t.Fatalf("page1: status = %d", w.Code)
	}
	page1 := decodeJSON[playHistoryResponse](t, w)
	if len(page1.Entries) != 2 {
		t.Fatalf("page1: expected 2, got %d", len(page1.Entries))
	}

	// Page 2: since=last rowid from page 1.
	cursor := page1.Entries[1].Rowid
	w = bearerGet(t, srv, fmt.Sprintf("/admin/playhistory?limit=2&since=%d", cursor), testCollectorToken)
	if w.Code != http.StatusOK {
		t.Fatalf("page2: status = %d", w.Code)
	}
	page2 := decodeJSON[playHistoryResponse](t, w)
	if len(page2.Entries) != 2 {
		t.Fatalf("page2: expected 2, got %d", len(page2.Entries))
	}
	if page2.Entries[0].Rowid <= cursor {
		t.Errorf("page2 first rowid %d should be > cursor %d", page2.Entries[0].Rowid, cursor)
	}

	// Page 3: should have 1 remaining.
	cursor = page2.Entries[1].Rowid
	w = bearerGet(t, srv, fmt.Sprintf("/admin/playhistory?limit=2&since=%d", cursor), testCollectorToken)
	page3 := decodeJSON[playHistoryResponse](t, w)
	if len(page3.Entries) != 1 {
		t.Fatalf("page3: expected 1, got %d", len(page3.Entries))
	}

	// Page 4: exhausted — empty.
	cursor = page3.Entries[0].Rowid
	w = bearerGet(t, srv, fmt.Sprintf("/admin/playhistory?limit=2&since=%d", cursor), testCollectorToken)
	page4 := decodeJSON[playHistoryResponse](t, w)
	if len(page4.Entries) != 0 {
		t.Fatalf("page4: expected 0, got %d", len(page4.Entries))
	}
}

func TestPlayHistoryLimitCap(t *testing.T) {
	srv := testServer(t, testServerOpts{collectorToken: testCollectorToken})
	seedData(t, srv)

	// Request limit=9999 — should be capped to 500.
	w := bearerGet(t, srv, "/admin/playhistory?limit=9999", testCollectorToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	// Just verify it doesn't error; with no scrobbles we get empty array.
	resp := decodeJSON[playHistoryResponse](t, w)
	if resp.Entries == nil {
		t.Error("entries should be empty array, not null")
	}
}

func TestPlayHistoryEmptyResult(t *testing.T) {
	srv := testServer(t, testServerOpts{collectorToken: testCollectorToken})

	w := bearerGet(t, srv, "/admin/playhistory", testCollectorToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	// Verify the JSON has entries as empty array, not null.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(raw["entries"]) == "null" {
		t.Error("entries should be [], not null")
	}
}
