package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	ListenAddr    string
	MusicDir      string
	DataDir       string // DB + cover art cache
	CoverArtDir   string // derived: DataDir/covers
	DBPath        string // derived: DataDir/preamp.db
	EncryptionKey string
	AuthDisabled  bool   // PREAMP_NO_AUTH=1: explicitly disable auth (dev only)
	DevUsername   string // PREAMP_DEV_USERNAME: seed a credential on startup
	DevPassword   string // PREAMP_DEV_PASSWORD: plaintext password for dev credential

	// Admin API (JSON, internal network only — trusted-header auth).
	AdminListenAddr string        // PREAMP_ADMIN_LISTEN (default ":4534")
	CredentialTTL   time.Duration // PREAMP_CREDENTIAL_TTL (default 168h = 7 days)
	CollectorToken  string        // PREAMP_COLLECTOR_TOKEN: bearer token for /admin/playhistory

	// Management UI auth — exactly one of OIDCIssuer or AdminSecretFile may be set.
	OIDCIssuer       string // PREAMP_OIDC_ISSUER
	OIDCClientID     string // PREAMP_OIDC_CLIENT_ID
	OIDCClientSecret string // PREAMP_OIDC_CLIENT_SECRET
	OIDCRedirectURI  string // PREAMP_OIDC_REDIRECT_URI
	AdminSecretFile  string // PREAMP_ADMIN_SECRET_FILE (username:password)
	ManageEnabled    bool   // derived: true if either OIDC or secret file configured
}

func Load() (*Config, error) {
	c := &Config{
		ListenAddr: envOr("PREAMP_LISTEN", ":4533"),
		MusicDir:   envOr("PREAMP_MUSIC_DIR", ""),
		DataDir:    envOr("PREAMP_DATA_DIR", "./data"),
	}

	if c.MusicDir == "" {
		return nil, fmt.Errorf("PREAMP_MUSIC_DIR is required")
	}

	info, err := os.Stat(c.MusicDir)
	if err != nil {
		return nil, fmt.Errorf("music dir: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("PREAMP_MUSIC_DIR is not a directory: %s", c.MusicDir)
	}

	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating data dir: %w", err)
	}

	c.DBPath = filepath.Join(c.DataDir, "preamp.db")
	c.CoverArtDir = filepath.Join(c.DataDir, "covers")

	if err := os.MkdirAll(c.CoverArtDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating cover art dir: %w", err)
	}

	c.AuthDisabled = envOr("PREAMP_NO_AUTH", "") == "1"
	c.EncryptionKey = envOr("PREAMP_ENCRYPTION_KEY", "")
	if c.EncryptionKey == "" && !c.AuthDisabled {
		return nil, fmt.Errorf("PREAMP_ENCRYPTION_KEY is required (or set PREAMP_NO_AUTH=1 for dev)")
	}
	if c.EncryptionKey != "" {
		keyBytes, err := hex.DecodeString(c.EncryptionKey)
		if err != nil {
			return nil, fmt.Errorf("PREAMP_ENCRYPTION_KEY is not valid hex: %w", err)
		}
		if n := len(keyBytes); n != 16 && n != 32 {
			return nil, fmt.Errorf("PREAMP_ENCRYPTION_KEY must decode to 16 or 32 bytes (AES-128/256), got %d", n)
		}
	}
	c.DevUsername = envOr("PREAMP_DEV_USERNAME", "")
	c.DevPassword = envOr("PREAMP_DEV_PASSWORD", "")

	// Admin API config.
	c.AdminListenAddr = envOr("PREAMP_ADMIN_LISTEN", ":4534")
	c.CollectorToken = envOr("PREAMP_COLLECTOR_TOKEN", "")

	// Management UI config.
	c.OIDCIssuer = envOr("PREAMP_OIDC_ISSUER", "")
	c.OIDCClientID = envOr("PREAMP_OIDC_CLIENT_ID", "")
	c.OIDCClientSecret = envOr("PREAMP_OIDC_CLIENT_SECRET", "")
	c.OIDCRedirectURI = envOr("PREAMP_OIDC_REDIRECT_URI", "")
	c.AdminSecretFile = envOr("PREAMP_ADMIN_SECRET_FILE", "")

	if c.OIDCIssuer != "" && c.AdminSecretFile != "" {
		return nil, fmt.Errorf("PREAMP_OIDC_ISSUER and PREAMP_ADMIN_SECRET_FILE are mutually exclusive")
	}

	c.ManageEnabled = c.OIDCIssuer != "" || c.AdminSecretFile != ""

	ttlStr := envOr("PREAMP_CREDENTIAL_TTL", "168h")
	c.CredentialTTL, err = time.ParseDuration(ttlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid PREAMP_CREDENTIAL_TTL %q: %w", ttlStr, err)
	}

	return c, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
