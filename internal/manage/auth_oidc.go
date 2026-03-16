package manage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCAuthenticator implements OIDC Authorization Code Flow with PKCE.
type OIDCAuthenticator struct {
	provider     *oidc.Provider
	oauth2Config oauth2.Config
	verifier     *oidc.IDTokenVerifier

	// Pending auth state: maps state → pkce verifier. Short-lived, cleaned by reaper.
	mu       sync.Mutex
	pending  map[string]*pendingAuth
	stop     chan struct{}
}

type pendingAuth struct {
	codeVerifier string
	createdAt    time.Time
}

// NewOIDCAuthenticator creates an OIDC authenticator using provider discovery.
func NewOIDCAuthenticator(ctx context.Context, issuerURL, clientID, clientSecret, redirectURI string) (*OIDCAuthenticator, error) {
	provider, err := oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery: %w", err)
	}

	oauth2Config := oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  redirectURI,
		Scopes:       []string{oidc.ScopeOpenID, "profile"},
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: clientID})

	a := &OIDCAuthenticator{
		provider:     provider,
		oauth2Config: oauth2Config,
		verifier:     verifier,
		pending:      make(map[string]*pendingAuth),
		stop:         make(chan struct{}),
	}
	go a.reapLoop()
	return a, nil
}

// Close stops the background reaper goroutine.
func (a *OIDCAuthenticator) Close() {
	close(a.stop)
}

// LoginHandler redirects to the OIDC authorization endpoint with PKCE.
func (a *OIDCAuthenticator) LoginHandler(w http.ResponseWriter, r *http.Request) {
	state := randomHex(16)
	codeVerifier := randomHex(32)

	a.mu.Lock()
	a.pending[state] = &pendingAuth{
		codeVerifier: codeVerifier,
		createdAt:    time.Now(),
	}
	a.mu.Unlock()

	codeChallenge := s256Challenge(codeVerifier)
	authURL := a.oauth2Config.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// CallbackHandler processes the OIDC callback, exchanges the code, and verifies the ID token.
func (a *OIDCAuthenticator) CallbackHandler(w http.ResponseWriter, r *http.Request) (string, error) {
	state := r.FormValue("state")
	code := r.FormValue("code")

	a.mu.Lock()
	pa, ok := a.pending[state]
	if ok {
		delete(a.pending, state)
	}
	a.mu.Unlock()

	if !ok {
		return "", fmt.Errorf("invalid or expired state")
	}

	ctx := r.Context()
	token, err := a.oauth2Config.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", pa.codeVerifier),
	)
	if err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return "", fmt.Errorf("no id_token in response")
	}

	idToken, err := a.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return "", fmt.Errorf("verifying id_token: %w", err)
	}

	var claims struct {
		PreferredUsername string `json:"preferred_username"`
		Sub              string `json:"sub"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return "", fmt.Errorf("parsing claims: %w", err)
	}

	username := claims.PreferredUsername
	if username == "" {
		username = claims.Sub
	}
	return username, nil
}

func s256Challenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func (a *OIDCAuthenticator) reapLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-a.stop:
			return
		case <-ticker.C:
			a.reapPending()
		}
	}
}

func (a *OIDCAuthenticator) reapPending() {
	cutoff := time.Now().Add(-10 * time.Minute)
	a.mu.Lock()
	for state, pa := range a.pending {
		if pa.createdAt.Before(cutoff) {
			delete(a.pending, state)
		}
	}
	a.mu.Unlock()
}
