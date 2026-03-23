package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadValid(t *testing.T) {
	musicDir := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "data")

	t.Setenv("PREAMP_MUSIC_DIR", musicDir)
	t.Setenv("PREAMP_DATA_DIR", dataDir)
	t.Setenv("PREAMP_LISTEN", "")
	t.Setenv("PREAMP_NO_AUTH", "1")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.MusicDir != musicDir {
		t.Errorf("MusicDir = %q, want %q", cfg.MusicDir, musicDir)
	}
	if cfg.ListenAddr != ":4533" {
		t.Errorf("ListenAddr = %q, want :4533", cfg.ListenAddr)
	}
	if !strings.HasSuffix(cfg.DBPath, "preamp.db") {
		t.Errorf("DBPath = %q, want suffix preamp.db", cfg.DBPath)
	}
	if !strings.HasSuffix(cfg.CoverArtDir, "covers") {
		t.Errorf("CoverArtDir = %q, want suffix covers", cfg.CoverArtDir)
	}

	// Verify directories were created.
	if _, err := os.Stat(dataDir); err != nil {
		t.Errorf("DataDir not created: %v", err)
	}
	if _, err := os.Stat(cfg.CoverArtDir); err != nil {
		t.Errorf("CoverArtDir not created: %v", err)
	}
}

func TestLoadMissingMusicDir(t *testing.T) {
	t.Setenv("PREAMP_MUSIC_DIR", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing PREAMP_MUSIC_DIR")
	}
	if !strings.Contains(err.Error(), "PREAMP_MUSIC_DIR is required") {
		t.Errorf("error = %q, want mention of PREAMP_MUSIC_DIR", err)
	}
}

func TestLoadMusicDirNotExists(t *testing.T) {
	t.Setenv("PREAMP_MUSIC_DIR", "/nonexistent/path/12345")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for nonexistent music dir")
	}
	if !strings.Contains(err.Error(), "music dir") {
		t.Errorf("error = %q, want mention of music dir", err)
	}
}

func TestLoadMusicDirIsFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "notadir")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	t.Setenv("PREAMP_MUSIC_DIR", f.Name())

	_, loadErr := Load()
	if loadErr == nil {
		t.Fatal("expected error when music dir is a file")
	}
	if !strings.Contains(loadErr.Error(), "not a directory") {
		t.Errorf("error = %q, want mention of not a directory", loadErr)
	}
}

func TestLoadMissingEncryptionKey(t *testing.T) {
	t.Setenv("PREAMP_MUSIC_DIR", t.TempDir())
	t.Setenv("PREAMP_ENCRYPTION_KEY", "")
	t.Setenv("PREAMP_NO_AUTH", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing PREAMP_ENCRYPTION_KEY")
	}
	if !strings.Contains(err.Error(), "PREAMP_ENCRYPTION_KEY is required") {
		t.Errorf("error = %q, want mention of PREAMP_ENCRYPTION_KEY", err)
	}
}

func TestLoadCustomListenAddr(t *testing.T) {
	t.Setenv("PREAMP_MUSIC_DIR", t.TempDir())
	t.Setenv("PREAMP_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	t.Setenv("PREAMP_LISTEN", ":9999")
	t.Setenv("PREAMP_NO_AUTH", "1")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != ":9999" {
		t.Errorf("ListenAddr = %q, want :9999", cfg.ListenAddr)
	}
}

func TestLoadCreatesDataDir(t *testing.T) {
	nested := filepath.Join(t.TempDir(), "a", "b", "c")
	t.Setenv("PREAMP_MUSIC_DIR", t.TempDir())
	t.Setenv("PREAMP_DATA_DIR", nested)
	t.Setenv("PREAMP_NO_AUTH", "1")

	_, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Errorf("nested DataDir not created: %v", err)
	}
}

func TestEnvOrFallback(t *testing.T) {
	t.Setenv("PREAMP_TEST_NONEXISTENT", "")
	if got := envOr("PREAMP_TEST_NONEXISTENT", "default"); got != "default" {
		t.Errorf("envOr = %q, want default", got)
	}
}

func TestEnvOrSet(t *testing.T) {
	t.Setenv("PREAMP_TEST_VAR", "custom")
	if got := envOr("PREAMP_TEST_VAR", "default"); got != "custom" {
		t.Errorf("envOr = %q, want custom", got)
	}
}

func TestLoadEncryptionKeyInvalidHex(t *testing.T) {
	t.Setenv("PREAMP_MUSIC_DIR", t.TempDir())
	t.Setenv("PREAMP_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	t.Setenv("PREAMP_ENCRYPTION_KEY", "not-valid-hex!")
	t.Setenv("PREAMP_NO_AUTH", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid hex encryption key")
	}
	if !strings.Contains(err.Error(), "not valid hex") {
		t.Errorf("error = %q, want mention of not valid hex", err)
	}
}

func TestLoadEncryptionKeyWrongLength(t *testing.T) {
	t.Setenv("PREAMP_MUSIC_DIR", t.TempDir())
	t.Setenv("PREAMP_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	t.Setenv("PREAMP_ENCRYPTION_KEY", "aabbccdd") // 4 bytes
	t.Setenv("PREAMP_NO_AUTH", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for wrong-length encryption key")
	}
	if !strings.Contains(err.Error(), "16 or 32 bytes") {
		t.Errorf("error = %q, want mention of 16 or 32 bytes", err)
	}
}

func TestLoadEncryptionKeyValid16Bytes(t *testing.T) {
	t.Setenv("PREAMP_MUSIC_DIR", t.TempDir())
	t.Setenv("PREAMP_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	t.Setenv("PREAMP_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef") // 16 bytes
	t.Setenv("PREAMP_NO_AUTH", "")

	_, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestLoadEncryptionKeyValid32Bytes(t *testing.T) {
	t.Setenv("PREAMP_MUSIC_DIR", t.TempDir())
	t.Setenv("PREAMP_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	t.Setenv("PREAMP_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef") // 32 bytes
	t.Setenv("PREAMP_NO_AUTH", "")

	_, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
}
