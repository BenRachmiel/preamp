package manage

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/BenRachmiel/preamp/internal/config"
	"github.com/BenRachmiel/preamp/internal/db"
)

func testManageServer(t *testing.T) (*ManageServer, string) {
	t.Helper()
	tmpDir := t.TempDir()

	secretPath := filepath.Join(tmpDir, "admin-secret")
	if err := os.WriteFile(secretPath, []byte("admin:testpass123"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		DataDir:         tmpDir,
		DBPath:          filepath.Join(tmpDir, "test.db"),
		EncryptionKey:   "0123456789abcdef0123456789abcdef",
		AdminSecretFile: secretPath,
		ManageEnabled:   true,
		CredentialTTL:   168 * time.Hour,
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	auth, err := NewSecretAuthenticator(secretPath)
	if err != nil {
		t.Fatalf("NewSecretAuthenticator: %v", err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	srv := NewServer(database, cfg, auth, log)
	t.Cleanup(func() { srv.Close() })

	return srv, tmpDir
}

// login performs a login and returns the session cookie.
func login(t *testing.T, srv *ManageServer) *http.Cookie {
	t.Helper()
	form := url.Values{"username": {"admin"}, "password": {"testpass123"}}
	req := httptest.NewRequest("POST", "/manage/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("login: status = %d, want 302; body: %s", w.Code, w.Body.String())
	}

	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatal("no session cookie after login")
	return nil
}

func TestLoginAndDashboard(t *testing.T) {
	srv, _ := testManageServer(t)
	cookie := login(t, srv)

	req := httptest.NewRequest("GET", "/manage/", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("dashboard: status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "credentials") {
		t.Error("dashboard should contain 'credentials'")
	}
}

func TestUnauthenticatedRedirect(t *testing.T) {
	srv, _ := testManageServer(t)

	req := httptest.NewRequest("GET", "/manage/", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 redirect", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/manage/login" {
		t.Errorf("redirect = %q, want /manage/login", loc)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	srv, _ := testManageServer(t)

	form := url.Values{"username": {"admin"}, "password": {"wrong"}}
	req := httptest.NewRequest("POST", "/manage/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestMintAPIKeyCredential(t *testing.T) {
	srv, _ := testManageServer(t)
	cookie := login(t, srv)

	form := url.Values{"client_name": {"Symfonium"}}
	req := httptest.NewRequest("POST", "/manage/credentials", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("mint: status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "API Key") {
		t.Error("response should show API Key for non-legacy credential")
	}
	if !strings.Contains(body, "Symfonium") {
		t.Error("response should mention client name")
	}
}

func TestMintLegacyCredential(t *testing.T) {
	srv, _ := testManageServer(t)
	cookie := login(t, srv)

	form := url.Values{"client_name": {"Supersonic"}, "legacy_auth": {"1"}}
	req := httptest.NewRequest("POST", "/manage/credentials", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("mint legacy: status = %d; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Password") {
		t.Error("legacy credential response should show Password")
	}
}

func TestFullCRUDFlow(t *testing.T) {
	srv, _ := testManageServer(t)
	cookie := login(t, srv)

	// Mint credential.
	form := url.Values{"client_name": {"TestClient"}}
	req := httptest.NewRequest("POST", "/manage/credentials", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("mint: status = %d", w.Code)
	}

	// Find the credential ID from the DB.
	conn, put, err := srv.db.ReadConn()
	if err != nil {
		t.Fatalf("ReadConn: %v", err)
	}
	var credID string
	sqlitex.ExecuteTransient(conn,
		`SELECT id FROM credential WHERE username = 'admin' AND client_name = 'TestClient'`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				credID = stmt.ColumnText(0)
				return nil
			},
		})
	put()
	if credID == "" {
		t.Fatal("credential not found in DB after mint")
	}

	// Renew.
	req = httptest.NewRequest("POST", "/manage/credentials/"+credID+"/renew", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("renew: status = %d; body: %s", w.Code, w.Body.String())
	}

	// Revoke.
	req = httptest.NewRequest("DELETE", "/manage/credentials/"+credID, nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("revoke: status = %d", w.Code)
	}

	// Verify deleted.
	conn, put, err = srv.db.ReadConn()
	if err != nil {
		t.Fatalf("ReadConn: %v", err)
	}
	var count int
	sqlitex.ExecuteTransient(conn,
		`SELECT count(*) FROM credential WHERE id = ?`,
		&sqlitex.ExecOptions{
			Args: []any{credID},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				count = stmt.ColumnInt(0)
				return nil
			},
		})
	put()
	if count != 0 {
		t.Error("credential should be deleted after revoke")
	}
}

func TestCrossUserBlocked(t *testing.T) {
	srv, _ := testManageServer(t)
	cookie := login(t, srv)

	// Insert a credential for a different user directly.
	conn, put, err := srv.db.WriteConn()
	if err != nil {
		t.Fatal(err)
	}
	sqlitex.ExecuteTransient(conn,
		`INSERT INTO credential (id, username, hashed_api_key, client_name) VALUES ('other-cred', 'bob', x'00', 'BobPhone')`,
		nil)
	put()

	// Try to renew bob's credential as admin.
	req := httptest.NewRequest("POST", "/manage/credentials/other-cred/renew", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	// Should fail because the WHERE clause includes username = session user.
	if w.Code == http.StatusOK && strings.Contains(w.Body.String(), "BobPhone") {
		t.Error("should not be able to renew another user's credential")
	}

	// Try to revoke.
	req = httptest.NewRequest("DELETE", "/manage/credentials/other-cred", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	// Verify bob's credential still exists.
	conn, put, err = srv.db.ReadConn()
	if err != nil {
		t.Fatal(err)
	}
	var count int
	sqlitex.ExecuteTransient(conn,
		`SELECT count(*) FROM credential WHERE id = 'other-cred'`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				count = stmt.ColumnInt(0)
				return nil
			},
		})
	put()
	if count != 1 {
		t.Error("bob's credential should still exist after admin's revoke attempt")
	}
}

func TestLogout(t *testing.T) {
	srv, _ := testManageServer(t)
	cookie := login(t, srv)

	req := httptest.NewRequest("POST", "/manage/logout", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("logout: status = %d, want 302", w.Code)
	}

	// Session should be invalidated.
	req = httptest.NewRequest("GET", "/manage/", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Error("should redirect to login after logout")
	}
}

func TestLoginPage(t *testing.T) {
	srv, _ := testManageServer(t)

	req := httptest.NewRequest("GET", "/manage/login", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("login page: status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "password") {
		t.Error("login page should contain password field")
	}
}
