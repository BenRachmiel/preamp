package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/BenRachmiel/preamp/internal/scanner"
)

// adminGet sends a GET to the admin handler with the given Remote-User header.
func adminGet(t *testing.T, srv *Server, path, user string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if user != "" {
		req.Header.Set("Remote-User", user)
	}
	w := httptest.NewRecorder()
	srv.AdminHandler().ServeHTTP(w, req)
	return w
}

// adminPost sends a POST with JSON body to the admin handler.
func adminPost(t *testing.T, srv *Server, path, user string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest("POST", path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.Header.Set("Remote-User", user)
	}
	w := httptest.NewRecorder()
	srv.AdminHandler().ServeHTTP(w, req)
	return w
}

// adminDelete sends a DELETE to the admin handler.
func adminDelete(t *testing.T, srv *Server, path, user string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("DELETE", path, nil)
	if user != "" {
		req.Header.Set("Remote-User", user)
	}
	w := httptest.NewRecorder()
	srv.AdminHandler().ServeHTTP(w, req)
	return w
}

func decodeJSON[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode JSON: %v\nbody: %s", err, w.Body.String())
	}
	return v
}

func TestAdminWhoami(t *testing.T) {
	srv := testServer(t)
	w := adminGet(t, srv, "/admin/whoami", "alice")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	resp := decodeJSON[map[string]string](t, w)
	if resp["username"] != "alice" {
		t.Errorf("username = %q, want alice", resp["username"])
	}
}

func TestAdminNoAuthModeDefaultUsername(t *testing.T) {
	srv := testServer(t) // AuthDisabled = true by default
	w := adminGet(t, srv, "/admin/whoami", "")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	resp := decodeJSON[map[string]string](t, w)
	if resp["username"] != "dev" {
		t.Errorf("username = %q, want dev", resp["username"])
	}
}

func TestAdminRequiresRemoteUser(t *testing.T) {
	srv := testServer(t, testServerOpts{encryptionKey: testEncryptionKey})
	w := adminGet(t, srv, "/admin/whoami", "")

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestAdminCreateCredential(t *testing.T) {
	srv := testServer(t, testServerOpts{encryptionKey: testEncryptionKey})
	body := map[string]any{
		"client_name": "symfonium",
		"legacy_auth": true,
		"ttl":         "24h",
	}
	w := adminPost(t, srv, "/admin/credentials", "alice", body)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201\nbody: %s", w.Code, w.Body.String())
	}
	cred := decodeJSON[adminCredential](t, w)
	if cred.Secret == "" {
		t.Error("expected secret in response")
	}
	if cred.ClientName != "symfonium" {
		t.Errorf("client_name = %q, want symfonium", cred.ClientName)
	}
	if !cred.LegacyAuth {
		t.Error("expected legacy_auth = true")
	}
	if cred.Username != "alice" {
		t.Errorf("username = %q, want alice", cred.Username)
	}
	if cred.ExpiresAt == "" {
		t.Error("expected expires_at to be set for ttl=24h")
	}
}

func TestAdminCreateCredentialNoTTL(t *testing.T) {
	srv := testServer(t, testServerOpts{encryptionKey: testEncryptionKey})
	body := map[string]any{
		"client_name": "feishin",
		"ttl":         "0",
	}
	w := adminPost(t, srv, "/admin/credentials", "alice", body)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}
	cred := decodeJSON[adminCredential](t, w)
	if cred.ExpiresAt != "" {
		t.Errorf("expected empty expires_at for ttl=0, got %q", cred.ExpiresAt)
	}
}

func TestAdminListCredentials(t *testing.T) {
	srv := testServer(t, testServerOpts{encryptionKey: testEncryptionKey})

	// Create 2 credentials.
	for _, name := range []string{"phone", "desktop"} {
		body := map[string]any{"client_name": name, "ttl": "24h"}
		w := adminPost(t, srv, "/admin/credentials", "alice", body)
		if w.Code != http.StatusCreated {
			t.Fatalf("create %s: status = %d", name, w.Code)
		}
	}

	w := adminGet(t, srv, "/admin/credentials", "alice")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	creds := decodeJSON[[]adminCredential](t, w)
	if len(creds) != 2 {
		t.Errorf("len = %d, want 2", len(creds))
	}
	// Verify secrets are NOT returned in list.
	for _, c := range creds {
		if c.Secret != "" {
			t.Errorf("secret should not be in list response")
		}
	}
}

func TestAdminRenewCredential(t *testing.T) {
	srv := testServer(t, testServerOpts{encryptionKey: testEncryptionKey})

	// Create.
	body := map[string]any{"client_name": "test", "ttl": "1h"}
	w := adminPost(t, srv, "/admin/credentials", "alice", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d", w.Code)
	}
	created := decodeJSON[adminCredential](t, w)

	// Renew.
	w = adminPost(t, srv, "/admin/credentials/"+created.ID+"/renew", "alice", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("renew: status = %d\nbody: %s", w.Code, w.Body.String())
	}
	renewed := decodeJSON[adminCredential](t, w)
	if renewed.ExpiresAt == "" {
		t.Error("expected expires_at after renew")
	}
	if renewed.ExpiresAt == created.ExpiresAt {
		t.Error("expires_at should have changed after renew")
	}
}

func TestAdminDeleteCredential(t *testing.T) {
	srv := testServer(t, testServerOpts{encryptionKey: testEncryptionKey})

	// Create.
	body := map[string]any{"client_name": "ephemeral", "ttl": "1h"}
	w := adminPost(t, srv, "/admin/credentials", "alice", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d", w.Code)
	}
	created := decodeJSON[adminCredential](t, w)

	// Delete.
	w = adminDelete(t, srv, "/admin/credentials/"+created.ID, "alice")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want 204", w.Code)
	}

	// Verify gone from list.
	w = adminGet(t, srv, "/admin/credentials", "alice")
	creds := decodeJSON[[]adminCredential](t, w)
	if len(creds) != 0 {
		t.Errorf("expected 0 credentials after delete, got %d", len(creds))
	}
}

func TestAdminCrossUserProtection(t *testing.T) {
	srv := testServer(t, testServerOpts{encryptionKey: testEncryptionKey})

	// Alice creates a credential.
	body := map[string]any{"client_name": "alice-phone", "ttl": "1h"}
	w := adminPost(t, srv, "/admin/credentials", "alice", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d", w.Code)
	}
	created := decodeJSON[adminCredential](t, w)

	// Bob tries to delete it.
	w = adminDelete(t, srv, "/admin/credentials/"+created.ID, "bob")
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-user delete: status = %d, want 404", w.Code)
	}

	// Bob tries to renew it.
	w = adminPost(t, srv, "/admin/credentials/"+created.ID+"/renew", "bob", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-user renew: status = %d, want 404", w.Code)
	}

	// Bob's list should be empty.
	w = adminGet(t, srv, "/admin/credentials", "bob")
	creds := decodeJSON[[]adminCredential](t, w)
	if len(creds) != 0 {
		t.Errorf("bob should see 0 credentials, got %d", len(creds))
	}
}

func TestAdminXForwardedUserHeader(t *testing.T) {
	srv := testServer(t, testServerOpts{encryptionKey: testEncryptionKey})

	req := httptest.NewRequest("GET", "/admin/whoami", nil)
	req.Header.Set("X-Forwarded-User", "charlie")
	w := httptest.NewRecorder()
	srv.AdminHandler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	resp := decodeJSON[map[string]string](t, w)
	if resp["username"] != "charlie" {
		t.Errorf("username = %q, want charlie", resp["username"])
	}
}

func TestAdminStats(t *testing.T) {
	srv := testServer(t)
	seedData(t, srv)

	w := adminGet(t, srv, "/admin/stats", "alice")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	stats := decodeJSON[adminStats](t, w)
	if stats.Artists != 2 {
		t.Errorf("artists = %d, want 2", stats.Artists)
	}
	if stats.Albums != 2 {
		t.Errorf("albums = %d, want 2", stats.Albums)
	}
	if stats.Songs != 3 {
		t.Errorf("songs = %d, want 3", stats.Songs)
	}
}

func TestAdminGetScanStatusNoScanner(t *testing.T) {
	srv := testServer(t)
	w := adminGet(t, srv, "/admin/scan", "alice")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	resp := decodeJSON[map[string]any](t, w)
	if resp["scanning"] != false {
		t.Errorf("scanning = %v, want false", resp["scanning"])
	}
	if resp["count"] != float64(0) {
		t.Errorf("count = %v, want 0", resp["count"])
	}
}

func TestAdminGetScanStatusWithScanner(t *testing.T) {
	srv := testServer(t)
	sc := scanner.New(srv.db, srv.cfg.MusicDir, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	srv.SetScanner(sc)

	w := adminGet(t, srv, "/admin/scan", "alice")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	resp := decodeJSON[map[string]any](t, w)
	if resp["scanning"] != false {
		t.Errorf("scanning = %v, want false", resp["scanning"])
	}
}

func TestAdminStartScanNoScanner(t *testing.T) {
	srv := testServer(t)
	w := adminPost(t, srv, "/admin/scan", "alice", nil)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestAdminStartScanWithScanner(t *testing.T) {
	srv := testServer(t)
	sc := scanner.New(srv.db, srv.cfg.MusicDir, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	srv.SetScanner(sc)

	w := adminPost(t, srv, "/admin/scan", "alice", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
	resp := decodeJSON[map[string]any](t, w)
	if resp["scanning"] != true {
		t.Errorf("scanning = %v, want true", resp["scanning"])
	}
}

func TestAdminStatsEmpty(t *testing.T) {
	srv := testServer(t)

	w := adminGet(t, srv, "/admin/stats", "alice")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	stats := decodeJSON[adminStats](t, w)
	if stats.Artists != 0 || stats.Albums != 0 || stats.Songs != 0 {
		t.Errorf("expected all zeros for empty db, got %+v", stats)
	}
}
