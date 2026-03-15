package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	ListenAddr   string
	MusicDir     string
	DataDir      string // DB + cover art cache
	CoverArtDir  string // derived: DataDir/covers
	DBPath       string // derived: DataDir/preamp.db
	EncryptionKey string
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

	c.EncryptionKey = envOr("PREAMP_ENCRYPTION_KEY", "")

	return c, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
