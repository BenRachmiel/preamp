package manage

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// mockOIDCProvider sets up a minimal OIDC provider for testing.
func mockOIDCProvider(t *testing.T) (*httptest.Server, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}

	mux := http.NewServeMux()

	var serverURL string

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 serverURL,
			"authorization_endpoint": serverURL + "/auth",
			"token_endpoint":         serverURL + "/token",
			"jwks_uri":               serverURL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})

	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		jwk := jose.JSONWebKey{Key: key.Public(), Algorithm: "RS256", Use: "sig", KeyID: "test-key"}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{jwk}})
	})

	srv := httptest.NewServer(mux)
	serverURL = srv.URL
	t.Cleanup(srv.Close)
	return srv, key
}

func signIDToken(t *testing.T, key *rsa.PrivateKey, issuer, clientID, username string) string {
	t.Helper()
	signerKey := jose.SigningKey{Algorithm: jose.RS256, Key: key}
	signer, err := jose.NewSigner(signerKey, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test-key"))
	if err != nil {
		t.Fatalf("creating signer: %v", err)
	}

	now := time.Now()
	claims := jwt.Claims{
		Issuer:    issuer,
		Subject:   "user-sub-123",
		Audience:  jwt.Audience{clientID},
		IssuedAt:  jwt.NewNumericDate(now),
		Expiry:    jwt.NewNumericDate(now.Add(time.Hour)),
	}
	extra := map[string]any{
		"preferred_username": username,
	}

	raw, err := jwt.Signed(signer).Claims(claims).Claims(extra).Serialize()
	if err != nil {
		t.Fatalf("signing JWT: %v", err)
	}
	return raw
}

func TestOIDCFullCallbackFlow(t *testing.T) {
	idpServer, key := mockOIDCProvider(t)

	clientID := "test-client"

	// Add token endpoint to the mock.
	idToken := signIDToken(t, key, idpServer.URL, clientID, "alice")

	tokenMux := idpServer.Config.Handler.(*http.ServeMux)
	tokenMux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "mock-access-token",
			"token_type":   "Bearer",
			"id_token":     idToken,
		})
	})

	auth, err := NewOIDCAuthenticator(
		t.Context(),
		idpServer.URL,
		clientID,
		"test-secret",
		"http://localhost/manage/callback",
	)
	if err != nil {
		t.Fatalf("NewOIDCAuthenticator: %v", err)
	}

	// LoginHandler should redirect.
	req := httptest.NewRequest("GET", "/manage/login", nil)
	w := httptest.NewRecorder()
	auth.LoginHandler(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("login: status = %d, want 302", w.Code)
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parsing redirect: %v", err)
	}
	state := loc.Query().Get("state")
	if state == "" {
		t.Fatal("no state in redirect")
	}
	codeChallenge := loc.Query().Get("code_challenge")
	if codeChallenge == "" {
		t.Fatal("no code_challenge (PKCE) in redirect")
	}

	// Simulate callback with the state.
	callbackURL := fmt.Sprintf("/manage/callback?state=%s&code=mock-auth-code", state)
	req = httptest.NewRequest("GET", callbackURL, nil)
	w = httptest.NewRecorder()
	username, err := auth.CallbackHandler(w, req)
	if err != nil {
		t.Fatalf("CallbackHandler: %v", err)
	}
	if username != "alice" {
		t.Errorf("username = %q, want alice", username)
	}
}

func TestOIDCInvalidState(t *testing.T) {
	idpServer, _ := mockOIDCProvider(t)

	auth, err := NewOIDCAuthenticator(
		t.Context(),
		idpServer.URL,
		"test-client",
		"test-secret",
		"http://localhost/manage/callback",
	)
	if err != nil {
		t.Fatalf("NewOIDCAuthenticator: %v", err)
	}

	req := httptest.NewRequest("GET", "/manage/callback?state=bogus&code=mock", nil)
	w := httptest.NewRecorder()
	_, err = auth.CallbackHandler(w, req)
	if err == nil {
		t.Fatal("expected error for invalid state")
	}
}

// Verify the signer implements crypto.Signer for jose.
var _ crypto.Signer = (*rsa.PrivateKey)(nil)
