package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestStreamHappyPath(t *testing.T) {
	srv := testServer(t)
	_, content := seedDataWithFiles(t, srv)

	w := get(t, srv, "/rest/stream?id=s1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "audio/mpeg" {
		t.Errorf("Content-Type = %q, want audio/mpeg", ct)
	}
	if w.Body.String() != string(content) {
		t.Errorf("body mismatch: got %d bytes, want %d", w.Body.Len(), len(content))
	}
}

func TestStreamMissingID(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/stream?")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestStreamSongNotFound(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/stream?id=nonexistent")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 70 {
		t.Errorf("error code = %v, want 70", apiErr["code"])
	}
}

func TestStreamFileMissingOnDisk(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv) // uses /fake/ paths that don't exist

	resp := getJSON(t, srv, "/rest/stream?id=s1")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status for missing file")
	}
}

func TestDownloadHappyPath(t *testing.T) {
	srv := testServer(t)
	_, content := seedDataWithFiles(t, srv)

	w := get(t, srv, "/rest/download?id=s1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != string(content) {
		t.Errorf("body mismatch")
	}
}

func TestDownloadMissingID(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/download?")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestGetCoverArtHappyPath(t *testing.T) {
	srv := testServer(t)
	seedDataWithFiles(t, srv)

	w := get(t, srv, "/rest/getCoverArt?id=alb1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if w.Body.String() != "fake-jpeg-data" {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestGetCoverArtWithPrefix(t *testing.T) {
	srv := testServer(t)
	seedDataWithFiles(t, srv)

	w := get(t, srv, "/rest/getCoverArt?id=al-alb1")
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (al- prefix should be stripped)", w.Code)
	}
}

func TestGetCoverArtAlbumNotFound(t *testing.T) {
	srv := testServer(t)
	w := get(t, srv, "/rest/getCoverArt?id=nonexistent")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestGetCoverArtNoCoverSet(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv) // albums have empty cover_art

	w := get(t, srv, "/rest/getCoverArt?id=alb1")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for album with no cover", w.Code)
	}
}

func TestGetCoverArtMissingID(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getCoverArt?")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestGetCoverArtResize(t *testing.T) {
	srv := testServer(t)
	seedDataWithRealCover(t, srv)

	w := get(t, srv, "/rest/getCoverArt?id=alb1&size=50")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	// Verify the resized file was cached to disk.
	resizedPath := filepath.Join(srv.cfg.CoverArtDir, "alb1_50.jpg")
	if _, err := os.Stat(resizedPath); err != nil {
		t.Errorf("resized file not cached: %v", err)
	}
}

func TestGetCoverArtResizeCached(t *testing.T) {
	srv := testServer(t)
	seedDataWithRealCover(t, srv)

	// First request creates the cached file.
	w1 := get(t, srv, "/rest/getCoverArt?id=alb1&size=100")
	if w1.Code != http.StatusOK {
		t.Fatalf("first request: status = %d", w1.Code)
	}

	// Record mtime of the cached file — should not change on second request.
	resizedPath := filepath.Join(srv.cfg.CoverArtDir, "alb1_100.jpg")
	stat1, err := os.Stat(resizedPath)
	if err != nil {
		t.Fatalf("cached file not found after first request: %v", err)
	}

	// Second request should serve from cache without regenerating.
	w2 := get(t, srv, "/rest/getCoverArt?id=alb1&size=100")
	if w2.Code != http.StatusOK {
		t.Fatalf("second request: status = %d", w2.Code)
	}

	stat2, err := os.Stat(resizedPath)
	if err != nil {
		t.Fatalf("cached file disappeared: %v", err)
	}
	if !stat1.ModTime().Equal(stat2.ModTime()) {
		t.Errorf("cached file was regenerated: mtime changed from %v to %v", stat1.ModTime(), stat2.ModTime())
	}
}

func TestGetCoverArtNoSize(t *testing.T) {
	srv := testServer(t)
	seedDataWithRealCover(t, srv)

	// Without size param, should serve original.
	w := get(t, srv, "/rest/getCoverArt?id=alb1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	// No resized files should exist.
	matches, _ := filepath.Glob(filepath.Join(srv.cfg.CoverArtDir, "alb1_*.jpg"))
	if len(matches) != 0 {
		t.Errorf("unexpected resized files: %v", matches)
	}
}

func TestGetCoverArtInvalidSize(t *testing.T) {
	srv := testServer(t)
	seedDataWithRealCover(t, srv)

	// Non-numeric size should serve the original without error.
	w := get(t, srv, "/rest/getCoverArt?id=alb1&size=abc")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for invalid size", w.Code)
	}

	// No resized files should exist.
	matches, _ := filepath.Glob(filepath.Join(srv.cfg.CoverArtDir, "alb1_*.jpg"))
	if len(matches) != 0 {
		t.Errorf("unexpected resized files for invalid size: %v", matches)
	}
}

func TestGetCoverArtSizeClamped(t *testing.T) {
	srv := testServer(t)
	seedDataWithRealCover(t, srv)

	// Size below minimum should be clamped to minCoverArtSize (32).
	w := get(t, srv, "/rest/getCoverArt?id=alb1&size=1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	// Should be clamped to min size, not size=1.
	clampedPath := filepath.Join(srv.cfg.CoverArtDir, "alb1_32.jpg")
	if _, err := os.Stat(clampedPath); err != nil {
		t.Errorf("expected clamped resize at min size: %v", err)
	}
	unclamped := filepath.Join(srv.cfg.CoverArtDir, "alb1_1.jpg")
	if _, err := os.Stat(unclamped); err == nil {
		t.Errorf("size=1 should have been clamped, but alb1_1.jpg exists")
	}
}
