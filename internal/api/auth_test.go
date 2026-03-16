package api

import (
	"encoding/hex"
	"fmt"
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
