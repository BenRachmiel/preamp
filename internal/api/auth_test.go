package api

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/BenRachmiel/preamp/internal/auth"
)

func TestAuthTokenSuccess(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")
	salt := "randomsalt"
	token := md5Hex("secret123" + salt)
	url := fmt.Sprintf("/rest/ping?f=json&u=alice&t=%s&s=%s", token, salt)

	resp := getJSON(t, srv, url)
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}
}

func TestAuthTokenWrongPassword(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")
	salt := "randomsalt"
	token := md5Hex("wrongpassword" + salt)
	url := fmt.Sprintf("/rest/ping?f=json&u=alice&t=%s&s=%s", token, salt)

	resp := getJSON(t, srv, url)
	if resp["status"] != "failed" {
		t.Errorf("expected failed status for wrong token")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 40 {
		t.Errorf("error code = %v, want 40", apiErr["code"])
	}
}

func TestAuthTokenWrongUsername(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")
	salt := "randomsalt"
	token := md5Hex("secret123" + salt)
	url := fmt.Sprintf("/rest/ping?f=json&u=bob&t=%s&s=%s", token, salt)

	resp := getJSON(t, srv, url)
	if resp["status"] != "failed" {
		t.Errorf("expected failed status for unknown user")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 40 {
		t.Errorf("error code = %v, want 40", apiErr["code"])
	}
}

func TestAuthLegacyPlaintext(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")
	url := "/rest/ping?f=json&u=alice&p=secret123"

	resp := getJSON(t, srv, url)
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}
}

func TestAuthLegacyHexEncoded(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")
	hexPass := hex.EncodeToString([]byte("secret123"))
	url := fmt.Sprintf("/rest/ping?f=json&u=alice&p=enc:%s", hexPass)

	resp := getJSON(t, srv, url)
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}
}

func TestAuthMissingUsername(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")

	resp := getJSON(t, srv, "/rest/ping?")
	if resp["status"] != "failed" {
		t.Errorf("expected failed status for missing username")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 10 {
		t.Errorf("error code = %v, want 10", apiErr["code"])
	}
}

func TestAuthMissingCredentials(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")
	url := "/rest/ping?f=json&u=alice"

	resp := getJSON(t, srv, url)
	if resp["status"] != "failed" {
		t.Errorf("expected failed status for missing credentials")
	}
}

func TestAuthDisabledFlag(t *testing.T) {
	// testServer sets AuthDisabled: true — requests should pass without credentials.
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/ping?")
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok (auth disabled)", resp["status"])
	}
}

func TestAuthExpiredCredential(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")

	// Update credential to be expired.
	conn, put, err := srv.db.WriteConn()
	if err != nil {
		t.Fatalf("WriteConn: %v", err)
	}
	err = sqlitex.ExecuteTransient(conn,
		`UPDATE credential SET expires_at = '2020-01-01T00:00:00' WHERE username = 'alice'`, nil)
	put()
	if err != nil {
		t.Fatalf("update credential: %v", err)
	}

	salt := "randomsalt"
	token := md5Hex("secret123" + salt)
	url := fmt.Sprintf("/rest/ping?f=json&u=alice&t=%s&s=%s", token, salt)

	resp := getJSON(t, srv, url)
	if resp["status"] != "failed" {
		t.Errorf("expected failed status for expired credential")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 40 {
		t.Errorf("error code = %v, want 40", apiErr["code"])
	}
}

func TestAuthRateLimiting(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")

	// Send 10 failed auth attempts from the same IP.
	for i := range 10 {
		url := fmt.Sprintf("/rest/ping?f=json&u=alice&t=wrong&s=salt%d", i)
		resp := getJSON(t, srv, url)
		if resp["status"] != "failed" {
			t.Fatalf("attempt %d: expected failed", i)
		}
	}

	// 11th attempt should be rate-limited (even with correct creds).
	salt := "ratelimittest"
	token := md5Hex("secret123" + salt)
	url := fmt.Sprintf("/rest/ping?f=json&u=alice&t=%s&s=%s", token, salt)
	resp := getJSON(t, srv, url)
	if resp["status"] != "failed" {
		t.Error("expected rate-limited request to fail")
	}
	apiErr := resp["error"].(map[string]any)
	if msg, ok := apiErr["message"].(string); !ok || msg != "too many failed attempts" {
		t.Errorf("error message = %v, want 'too many failed attempts'", apiErr["message"])
	}
}

func TestAuthRateLimitClearsOnSuccess(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")

	// Send 5 failed attempts.
	for i := range 5 {
		url := fmt.Sprintf("/rest/ping?f=json&u=alice&t=wrong&s=salt%d", i)
		getJSON(t, srv, url)
	}

	// A successful auth should clear the counter.
	salt := "clearsalt"
	token := md5Hex("secret123" + salt)
	url := fmt.Sprintf("/rest/ping?f=json&u=alice&t=%s&s=%s", token, salt)
	resp := getJSON(t, srv, url)
	if resp["status"] != "ok" {
		t.Error("expected success after partial failures")
	}

	// Now send 10 more failures — should be allowed since counter was cleared.
	for i := range 10 {
		url := fmt.Sprintf("/rest/ping?f=json&u=alice&t=wrong&s=after%d", i)
		getJSON(t, srv, url)
	}

	// 11th should now be rate-limited.
	resp = getJSON(t, srv, "/rest/ping?f=json&u=alice&t=wrong&s=final")
	if resp["status"] != "failed" {
		t.Error("expected rate limit after 10 failures post-clear")
	}
}

func TestEncryptDecryptPasswordRoundTrip(t *testing.T) {
	password := "test-password-123"
	encrypted, err := auth.EncryptPassword(testEncryptionKey, password)
	if err != nil {
		t.Fatalf("EncryptPassword: %v", err)
	}

	decrypted, err := auth.DecryptPassword(testEncryptionKey, encrypted)
	if err != nil {
		t.Fatalf("DecryptPassword: %v", err)
	}

	if decrypted != password {
		t.Errorf("round-trip failed: got %q, want %q", decrypted, password)
	}
}

func TestAuthLegacyPlaintextWrongPassword(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")

	resp := getJSON(t, srv, "/rest/ping?u=alice&p=wrongpassword")
	if resp["status"] != "failed" {
		t.Errorf("expected failed status for wrong plaintext password")
	}
	apiErr, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error in response")
	}
	if apiErr["code"].(float64) != 40 {
		t.Errorf("error code = %v, want 40", apiErr["code"])
	}
}

func TestAuthLegacyHexMalformed(t *testing.T) {
	srv := testServerWithAuth(t, "alice", "secret123")

	resp := getJSON(t, srv, "/rest/ping?u=alice&p=enc:ZZZZ")
	if resp["status"] != "failed" {
		t.Errorf("expected failed status for malformed hex password")
	}
	apiErr, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error in response")
	}
	if apiErr["code"].(float64) != 40 {
		t.Errorf("error code = %v, want 40", apiErr["code"])
	}
}

// --- apiKey auth tests ---

func TestAuthAPIKeySuccess(t *testing.T) {
	srv := testServer(t, testServerOpts{encryptionKey: testEncryptionKey})
	password := "my-api-key-secret"
	seedCredential(t, srv, "alice", password, false)

	resp := getJSON(t, srv, "/rest/ping?apiKey="+password)
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}
}

func TestAuthAPIKeyWrongKey(t *testing.T) {
	srv := testServer(t, testServerOpts{encryptionKey: testEncryptionKey})
	seedCredential(t, srv, "alice", "correct-key", false)

	resp := getJSON(t, srv, "/rest/ping?apiKey=wrong-key")
	if resp["status"] != "failed" {
		t.Errorf("expected failed for wrong apiKey")
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 40 {
		t.Errorf("error code = %v, want 40", apiErr["code"])
	}
}

func TestAuthAPIKeyExpired(t *testing.T) {
	srv := testServer(t, testServerOpts{encryptionKey: testEncryptionKey})
	seedCredential(t, srv, "alice", "my-key", false)

	conn, put, err := srv.db.WriteConn()
	if err != nil {
		t.Fatalf("WriteConn: %v", err)
	}
	err = sqlitex.ExecuteTransient(conn,
		`UPDATE credential SET expires_at = '2020-01-01T00:00:00' WHERE username = 'alice'`, nil)
	put()
	if err != nil {
		t.Fatalf("update credential: %v", err)
	}

	resp := getJSON(t, srv, "/rest/ping?apiKey=my-key")
	if resp["status"] != "failed" {
		t.Errorf("expected failed for expired apiKey credential")
	}
}

func TestAuthAPIKeyNoUsernameRequired(t *testing.T) {
	srv := testServer(t, testServerOpts{encryptionKey: testEncryptionKey})
	seedCredential(t, srv, "alice", "my-api-key", false)

	// apiKey auth should work without u= param
	resp := getJSON(t, srv, "/rest/ping?apiKey=my-api-key")
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok (apiKey should not require u=)", resp["status"])
	}
}

func TestAuthMultiCredentialSameUser(t *testing.T) {
	srv := testServer(t, testServerOpts{encryptionKey: testEncryptionKey})
	seedCredential(t, srv, "alice", "key-for-phone", true)
	seedCredential(t, srv, "alice", "key-for-desktop", true)

	// Both should work via token auth
	salt := "s1"
	token1 := md5Hex("key-for-phone" + salt)
	resp := getJSON(t, srv, fmt.Sprintf("/rest/ping?u=alice&t=%s&s=%s", token1, salt))
	if resp["status"] != "ok" {
		t.Errorf("first credential: status = %v, want ok", resp["status"])
	}

	token2 := md5Hex("key-for-desktop" + salt)
	resp = getJSON(t, srv, fmt.Sprintf("/rest/ping?u=alice&t=%s&s=%s", token2, salt))
	if resp["status"] != "ok" {
		t.Errorf("second credential: status = %v, want ok", resp["status"])
	}

	// Both should also work via apiKey
	resp = getJSON(t, srv, "/rest/ping?apiKey=key-for-phone")
	if resp["status"] != "ok" {
		t.Errorf("apiKey first: status = %v, want ok", resp["status"])
	}
	resp = getJSON(t, srv, "/rest/ping?apiKey=key-for-desktop")
	if resp["status"] != "ok" {
		t.Errorf("apiKey second: status = %v, want ok", resp["status"])
	}
}

func TestAuthExpiredAmongMultiple(t *testing.T) {
	srv := testServer(t, testServerOpts{encryptionKey: testEncryptionKey})
	seedCredential(t, srv, "alice", "expired-key", true)
	seedCredential(t, srv, "alice", "valid-key", true)

	// Expire the first credential
	conn, put, err := srv.db.WriteConn()
	if err != nil {
		t.Fatalf("WriteConn: %v", err)
	}
	err = sqlitex.ExecuteTransient(conn,
		`UPDATE credential SET expires_at = '2020-01-01T00:00:00'
		 WHERE username = 'alice' AND id = (SELECT id FROM credential WHERE username = 'alice' LIMIT 1)`, nil)
	put()
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	// Valid key should still work
	salt := "s1"
	token := md5Hex("valid-key" + salt)
	resp := getJSON(t, srv, fmt.Sprintf("/rest/ping?u=alice&t=%s&s=%s", token, salt))
	if resp["status"] != "ok" {
		t.Errorf("valid key among mixed: status = %v, want ok", resp["status"])
	}
}

func TestAuthAPIKeyCacheHit(t *testing.T) {
	srv := testServer(t, testServerOpts{encryptionKey: testEncryptionKey})
	seedCredential(t, srv, "alice", "cached-key", false)

	// First request — populates cache via bcrypt scan.
	resp := getJSON(t, srv, "/rest/ping?apiKey=cached-key")
	if resp["status"] != "ok" {
		t.Fatalf("first request: status = %v, want ok", resp["status"])
	}

	// Second request — served from cache (same key, no bcrypt).
	resp = getJSON(t, srv, "/rest/ping?apiKey=cached-key")
	if resp["status"] != "ok" {
		t.Errorf("cached request: status = %v, want ok", resp["status"])
	}
}

func TestAuthAPIKeyCacheInvalidatedOnDelete(t *testing.T) {
	srv := testServer(t, testServerOpts{encryptionKey: testEncryptionKey})
	credID := seedCredential(t, srv, "alice", "delete-me-key", false)

	// Authenticate to populate cache.
	resp := getJSON(t, srv, "/rest/ping?apiKey=delete-me-key")
	if resp["status"] != "ok" {
		t.Fatalf("initial auth: status = %v, want ok", resp["status"])
	}

	// Delete credential via admin handler.
	req := httptest.NewRequest("DELETE", "/admin/credentials/"+credID, nil)
	req.Header.Set("Remote-User", "alice")
	w := httptest.NewRecorder()
	srv.AdminHandler().ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete credential: status %d, want 204", w.Code)
	}

	// Re-authenticate — should fail (cache was flushed, credential gone).
	resp = getJSON(t, srv, "/rest/ping?apiKey=delete-me-key")
	if resp["status"] != "failed" {
		t.Errorf("post-delete auth: status = %v, want failed", resp["status"])
	}
	apiErr := resp["error"].(map[string]any)
	if apiErr["code"].(float64) != 40 {
		t.Errorf("error code = %v, want 40", apiErr["code"])
	}
}

func TestAuthAllCredentialsExpired(t *testing.T) {
	srv := testServer(t, testServerOpts{encryptionKey: testEncryptionKey})
	seedCredential(t, srv, "alice", "key1", true)
	seedCredential(t, srv, "alice", "key2", true)

	conn, put, err := srv.db.WriteConn()
	if err != nil {
		t.Fatalf("WriteConn: %v", err)
	}
	err = sqlitex.ExecuteTransient(conn,
		`UPDATE credential SET expires_at = '2020-01-01T00:00:00' WHERE username = 'alice'`, nil)
	put()
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	// Token auth should fail
	salt := "s1"
	token := md5Hex("key1" + salt)
	resp := getJSON(t, srv, fmt.Sprintf("/rest/ping?u=alice&t=%s&s=%s", token, salt))
	if resp["status"] != "failed" {
		t.Errorf("expected failed when all credentials expired")
	}

	// apiKey auth should also fail
	resp = getJSON(t, srv, "/rest/ping?apiKey=key1")
	if resp["status"] != "failed" {
		t.Errorf("expected failed for expired apiKey")
	}
}
