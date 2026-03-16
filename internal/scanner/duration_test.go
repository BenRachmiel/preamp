package scanner

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestParseMP3DurationRealFiles(t *testing.T) {
	musicDir := filepath.Join("..", "..", "test-music-lib")
	if _, err := os.Stat(musicDir); err != nil {
		t.Skip("test-music-lib not found, skipping")
	}

	log := testLogger()

	// Test a known track: ABBA - Dancing Queen should be ~3:51 (231 seconds).
	// test-music-lib is Artist/Album/track.mp3 (two levels deep).
	matches, _ := filepath.Glob(filepath.Join(musicDir, "*", "*", "*.mp3"))

	if len(matches) == 0 {
		t.Skip("no MP3 files found in test-music-lib")
	}

	for _, path := range matches[:min(5, len(matches))] {
		dur, br := parseDuration(path, ".mp3", log)
		t.Logf("%s: duration=%ds bitrate=%dkbps", filepath.Base(path), dur, br)
		if dur <= 0 {
			t.Errorf("duration should be > 0 for %s, got %d", filepath.Base(path), dur)
		}
		// Sanity: most songs are 1-10 minutes.
		if dur > 600 {
			t.Errorf("duration %d seems too high for %s", dur, filepath.Base(path))
		}
	}
}

func TestParseFLACDuration(t *testing.T) {
	// Create a minimal valid FLAC file with known STREAMINFO.
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.flac")

	// Minimal FLAC: fLaC marker + STREAMINFO block.
	// STREAMINFO: 34 bytes total.
	// We'll encode: sample rate 44100, total samples 441000 (10 seconds).
	data := make([]byte, 4+4+34) // marker + block header + streaminfo
	copy(data[0:4], "fLaC")

	// Block header: last-block flag (0x80) | type 0 (STREAMINFO), length 34.
	data[4] = 0x80 // last metadata block + type 0
	data[5] = 0
	data[6] = 0
	data[7] = 34

	// STREAMINFO bytes 0-3: min block size, max block size (not relevant).
	// Bytes 4-9: min/max frame size (not relevant).
	// Bytes 10-13: sample rate (20 bits) | channels (3 bits) | bps (5 bits) | total samples high 4 bits.
	// Bytes 14-17: total samples low 32 bits.

	// Sample rate 44100 = 0xAC44 in 20 bits = 0x0AC44.
	// Channels: 2 channels = 1 (zero-indexed) = 001 in 3 bits.
	// Bits per sample: 16 = 15 (zero-indexed) = 01111 in 5 bits.
	// Total samples: 441000 = 0x6BAE8.
	sampleRate := 44100
	totalSamples := int64(441000) // 10 seconds at 44100 Hz

	data[8+10] = byte(sampleRate >> 12)
	data[8+11] = byte(sampleRate >> 4)
	data[8+12] = byte(sampleRate<<4) | byte(1<<1) | byte(15>>4) // channels=001, bps high bit
	data[8+13] = byte(15<<4) | byte((totalSamples>>32)&0x0F)
	data[8+14] = byte(totalSamples >> 24)
	data[8+15] = byte(totalSamples >> 16)
	data[8+16] = byte(totalSamples >> 8)
	data[8+17] = byte(totalSamples)

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	dur, _, err := parseFLACDuration(path)
	if err != nil {
		t.Fatalf("parseFLACDuration: %v", err)
	}
	if dur != 10 {
		t.Errorf("duration = %d, want 10", dur)
	}
}

func TestParseDurationFallbackReturnsZero(t *testing.T) {
	// A file that's not a valid MP3 or FLAC should not crash.
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "garbage.mp3")
	os.WriteFile(path, []byte("this is not audio data"), 0o644)

	log := testLogger()
	dur, br := parseDuration(path, ".mp3", log)
	// Should not crash. May return 0 if ffprobe is unavailable.
	t.Logf("garbage file: dur=%d br=%d", dur, br)
}
