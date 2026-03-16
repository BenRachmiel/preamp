package manage

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSecretFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "admin-secret")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing secret file: %v", err)
	}
	return path
}

func TestSecretAuthValidLogin(t *testing.T) {
	path := writeSecretFile(t, "admin:hunter2\n")
	auth, err := NewSecretAuthenticator(path)
	if err != nil {
		t.Fatalf("NewSecretAuthenticator: %v", err)
	}

	form := url.Values{"username": {"admin"}, "password": {"hunter2"}}
	req := httptest.NewRequest("POST", "/manage/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	username, err := auth.CallbackHandler(w, req)
	if err != nil {
		t.Fatalf("CallbackHandler: %v", err)
	}
	if username != "admin" {
		t.Errorf("username = %q, want admin", username)
	}
}

func TestSecretAuthWrongPassword(t *testing.T) {
	path := writeSecretFile(t, "admin:hunter2\n")
	auth, err := NewSecretAuthenticator(path)
	if err != nil {
		t.Fatalf("NewSecretAuthenticator: %v", err)
	}

	form := url.Values{"username": {"admin"}, "password": {"wrong"}}
	req := httptest.NewRequest("POST", "/manage/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	_, err = auth.CallbackHandler(w, req)
	if err == nil {
		t.Fatal("expected error for wrong password")
	}
}

func TestSecretAuthWrongUsername(t *testing.T) {
	path := writeSecretFile(t, "admin:hunter2\n")
	auth, err := NewSecretAuthenticator(path)
	if err != nil {
		t.Fatalf("NewSecretAuthenticator: %v", err)
	}

	form := url.Values{"username": {"hacker"}, "password": {"hunter2"}}
	req := httptest.NewRequest("POST", "/manage/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	_, err = auth.CallbackHandler(w, req)
	if err == nil {
		t.Fatal("expected error for wrong username")
	}
}

func TestSecretAuthMalformedFile(t *testing.T) {
	path := writeSecretFile(t, "no-colon-here\n")
	_, err := NewSecretAuthenticator(path)
	if err == nil {
		t.Fatal("expected error for malformed file")
	}
}

func TestSecretAuthEmptyPassword(t *testing.T) {
	path := writeSecretFile(t, "admin:\n")
	_, err := NewSecretAuthenticator(path)
	if err == nil {
		t.Fatal("expected error for empty password")
	}
}

func TestSecretAuthMissingFile(t *testing.T) {
	_, err := NewSecretAuthenticator("/nonexistent/path/to/file")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestSecretAuthLoginPage(t *testing.T) {
	path := writeSecretFile(t, "admin:hunter2\n")
	auth, err := NewSecretAuthenticator(path)
	if err != nil {
		t.Fatalf("NewSecretAuthenticator: %v", err)
	}

	req := httptest.NewRequest("GET", "/manage/login", nil)
	w := httptest.NewRecorder()
	auth.LoginHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "password") {
		t.Error("login page should contain a password field")
	}
}
