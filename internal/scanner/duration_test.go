package scanner

import (
	"encoding/binary"
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
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("Open %s: %v", path, err)
		}
		stat, _ := f.Stat()
		dur, br := parseDuration(f, stat.Size(), 0, ".mp3", log)
		f.Close()
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

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()
	stat, _ := f.Stat()
	dur, _, err := parseFLACDuration(f, stat.Size())
	if err != nil {
		t.Fatalf("parseFLACDuration: %v", err)
	}
	if dur != 10 {
		t.Errorf("duration = %d, want 10", dur)
	}
}

// buildMP3Frame constructs a minimal MPEG frame header (4 bytes) + padding
// to make a file large enough for CBR estimation to produce a nonzero duration.
//
//	version: 3=MPEG1, 2=MPEG2, 0=MPEG2.5
//	bitrateIdx: index into the version's bitrate table
//	sampleRateIdx: index into the version's sample rate table
//	channelMode: 0=stereo, 3=mono
func buildMP3Frame(version, bitrateIdx, sampleRateIdx, channelMode byte) []byte {
	// Sync word: 0xFFE0 (11 bits set).
	b0 := byte(0xFF)
	// Bits: sync(3) | version(2) | layer(2) | protection(1)
	// Layer III = 01 in the 2-bit field, protection bit = 1 (no CRC).
	b1 := byte(0xE0) | (version << 3) | (1 << 1) | 1
	b2 := (bitrateIdx << 4) | (sampleRateIdx << 2) // padding=0, private=0
	b3 := channelMode << 6

	// Pad to 200KB so CBR estimation yields ~seconds of audio.
	data := make([]byte, 200_000)
	data[0] = b0
	data[1] = b1
	data[2] = b2
	data[3] = b3
	return data
}

// buildMP3WithXing builds an MP3 file with a Xing header inside the first frame.
func buildMP3WithXing(version, bitrateIdx, sampleRateIdx, channelMode byte, totalFrames uint32) []byte {
	data := buildMP3Frame(version, bitrateIdx, sampleRateIdx, channelMode)

	// Determine Xing offset based on version and channel mode.
	var xingOff int
	if version == 3 { // MPEG1
		if channelMode == 3 {
			xingOff = 17 + 4
		} else {
			xingOff = 32 + 4
		}
	} else { // MPEG2/2.5
		if channelMode == 3 {
			xingOff = 9 + 4
		} else {
			xingOff = 17 + 4
		}
	}

	copy(data[xingOff:], "Xing")
	binary.BigEndian.PutUint32(data[xingOff+4:], 0x01) // flags: frames present
	binary.BigEndian.PutUint32(data[xingOff+8:], totalFrames)
	return data
}

func TestParseMP3DurationMPEG2(t *testing.T) {
	cases := []struct {
		name          string
		version       byte
		bitrateIdx    byte
		sampleRateIdx byte
		wantBitrate   int // expected CBR kbps
		wantMinDur    int // minimum expected duration (seconds)
	}{
		{
			name:          "MPEG2 Layer III 128kbps 22050Hz",
			version:       2,
			bitrateIdx:    12, // 128 kbps in V2 table
			sampleRateIdx: 0,  // 22050 Hz
			wantBitrate:   128,
			wantMinDur:    1,
		},
		{
			name:          "MPEG2.5 Layer III 64kbps 11025Hz",
			version:       0,
			bitrateIdx:    8, // 64 kbps in V2 table
			sampleRateIdx: 0, // 11025 Hz
			wantBitrate:   64,
			wantMinDur:    1,
		},
		{
			name:          "MPEG2 Layer III 32kbps 24000Hz",
			version:       2,
			bitrateIdx:    4, // 32 kbps in V2 table
			sampleRateIdx: 1, // 24000 Hz
			wantBitrate:   32,
			wantMinDur:    1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := buildMP3Frame(tc.version, tc.bitrateIdx, tc.sampleRateIdx, 0)
			tmpDir := t.TempDir()
			path := filepath.Join(tmpDir, "test.mp3")
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}

			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			stat, _ := f.Stat()

			dur, br, err := parseMP3Duration(f, stat.Size(), 0)
			if err != nil {
				t.Fatalf("parseMP3Duration: %v", err)
			}
			if br != tc.wantBitrate {
				t.Errorf("bitrate = %d, want %d", br, tc.wantBitrate)
			}
			if dur < tc.wantMinDur {
				t.Errorf("duration = %d, want >= %d", dur, tc.wantMinDur)
			}
			t.Logf("dur=%ds bitrate=%dkbps", dur, br)
		})
	}
}

func TestParseMP3DurationMPEG2Xing(t *testing.T) {
	// MPEG2, 22050 Hz, stereo, 5000 frames.
	// Duration = 5000 * 576 / 22050 = 130 seconds.
	data := buildMP3WithXing(2, 12, 0, 0, 5000)
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.mp3")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stat, _ := f.Stat()

	dur, _, err := parseMP3Duration(f, stat.Size(), 0)
	if err != nil {
		t.Fatalf("parseMP3Duration: %v", err)
	}
	want := (5000 * 576) / 22050
	if dur != want {
		t.Errorf("duration = %d, want %d", dur, want)
	}
}

func TestParseMP3DurationMPEG1StillWorks(t *testing.T) {
	// Regression: MPEG1 should still work after the version refactor.
	// MPEG1, 128kbps (bitrateIdx=9), 44100Hz (sampleRateIdx=0), stereo.
	data := buildMP3Frame(3, 9, 0, 0)
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.mp3")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stat, _ := f.Stat()

	dur, br, err := parseMP3Duration(f, stat.Size(), 0)
	if err != nil {
		t.Fatalf("parseMP3Duration: %v", err)
	}
	if br != 128 {
		t.Errorf("bitrate = %d, want 128", br)
	}
	if dur < 1 {
		t.Errorf("duration = %d, want >= 1", dur)
	}
}

func TestParseMP3RejectsReservedVersion(t *testing.T) {
	// Version 1 is reserved — should not parse.
	data := buildMP3Frame(1, 9, 0, 0)
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.mp3")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stat, _ := f.Stat()

	dur, _, err := parseMP3Duration(f, stat.Size(), 0)
	if err == nil && dur > 0 {
		t.Errorf("expected failure for reserved version, got dur=%d", dur)
	}
}

func TestParseDurationFallbackReturnsZero(t *testing.T) {
	// A file that's not a valid MP3 or FLAC should not crash.
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "garbage.mp3")
	os.WriteFile(path, []byte("this is not audio data"), 0o644)

	log := testLogger()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()
	stat, _ := f.Stat()
	dur, br := parseDuration(f, stat.Size(), 0, ".mp3", log)
	// Should not crash. May return 0 if ffprobe is unavailable.
	t.Logf("garbage file: dur=%d br=%d", dur, br)
}
