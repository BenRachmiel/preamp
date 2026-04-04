package api

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"zombiezen.com/go/sqlite/sqlitex"
)

// buildCBRMP3 creates a minimal CBR MP3 file with the given number of frames.
// MPEG1 Layer III, 128 kbps, 44100 Hz, stereo, no padding.
// Frame size = floor(144 * 128000 / 44100) = 417 bytes.
func buildCBRMP3(t *testing.T, numFrames int) []byte {
	t.Helper()
	const frameSize = 417
	// 0xFF 0xFB = sync(11) + MPEG1(2) + Layer III(2) + no CRC(1)
	// 0x90     = bitrate index 9 (128kbps) + sample rate 0 (44100) + no padding + private 0
	// 0x00     = stereo + mode ext 0 + no copyright + no original + no emphasis
	header := [4]byte{0xFF, 0xFB, 0x90, 0x00}

	var buf bytes.Buffer
	for range numFrames {
		buf.Write(header[:])
		buf.Write(make([]byte, frameSize-4))
	}
	return buf.Bytes()
}

// buildVBRMP3 creates a minimal VBR MP3 file with a Xing header and TOC.
func buildVBRMP3(t *testing.T, numFrames int) []byte {
	t.Helper()
	const frameSize = 417
	header := [4]byte{0xFF, 0xFB, 0x90, 0x00}

	var buf bytes.Buffer

	// First frame: contains Xing header at stereo offset (32+4 = 36 bytes from frame start).
	buf.Write(header[:])
	// Pad to offset 36
	buf.Write(make([]byte, 32)) // side info placeholder

	// Xing header: tag + flags + frames + bytes + TOC
	buf.WriteString("Xing")
	flags := uint32(0x07) // frames + bytes + TOC
	binary.Write(&buf, binary.BigEndian, flags)
	binary.Write(&buf, binary.BigEndian, uint32(numFrames))
	totalBytes := uint32(numFrames * frameSize)
	binary.Write(&buf, binary.BigEndian, totalBytes)

	// TOC: 100 entries, linear mapping (entry[i] = i * 256 / 100)
	for i := range 100 {
		buf.WriteByte(byte(i * 256 / 100))
	}

	// Pad rest of first frame
	remaining := frameSize - (4 + 32 + 4 + 4 + 4 + 4 + 100)
	if remaining > 0 {
		buf.Write(make([]byte, remaining))
	}

	// Remaining audio frames
	for range numFrames - 1 {
		buf.Write(header[:])
		buf.Write(make([]byte, frameSize-4))
	}
	return buf.Bytes()
}

// buildTestFLAC creates a minimal FLAC file with STREAMINFO + SEEKTABLE.
func buildTestFLAC(t *testing.T, sampleRate int, totalSamples int64, seekPoints []flacSeekPoint, audioData []byte) []byte {
	t.Helper()
	var buf bytes.Buffer

	// fLaC marker
	buf.WriteString("fLaC")

	// STREAMINFO metadata block (type 0)
	var si [34]byte
	// Bytes 10-12: sample rate (20 bits) + channels (3 bits) + bps (5 bits)
	si[10] = byte(sampleRate >> 12)
	si[11] = byte(sampleRate >> 4)
	si[12] = byte(sampleRate<<4) | 0x02 // 1 channel (0 = 1ch), 16 bps (15 = 16 bits)
	// Bytes 13-17: total samples (36 bits)
	si[13] = byte(totalSamples>>32) & 0x0F
	si[14] = byte(totalSamples >> 24)
	si[15] = byte(totalSamples >> 16)
	si[16] = byte(totalSamples >> 8)
	si[17] = byte(totalSamples)

	if len(seekPoints) > 0 {
		// STREAMINFO is NOT last (bit 7 = 0)
		buf.Write([]byte{0x00, 0, 0, 34})
	} else {
		// STREAMINFO IS last
		buf.Write([]byte{0x80, 0, 0, 34})
	}
	buf.Write(si[:])

	// SEEKTABLE metadata block (type 3)
	if len(seekPoints) > 0 {
		seekLen := len(seekPoints) * 18
		// Last metadata block
		buf.WriteByte(0x80 | 3)
		buf.WriteByte(byte(seekLen >> 16))
		buf.WriteByte(byte(seekLen >> 8))
		buf.WriteByte(byte(seekLen))
		for _, sp := range seekPoints {
			binary.Write(&buf, binary.BigEndian, sp.sampleNumber)
			binary.Write(&buf, binary.BigEndian, sp.byteOffset)
			binary.Write(&buf, binary.BigEndian, sp.numSamples)
		}
	}

	buf.Write(audioData)
	return buf.Bytes()
}

// buildTestWAV creates a minimal PCM WAV file.
func buildTestWAV(t *testing.T, sampleRate, channels, bitsPerSample int, pcmData []byte) []byte {
	t.Helper()
	blockAlign := channels * (bitsPerSample / 8)
	byteRate := sampleRate * blockAlign
	fmtSize := 16
	dataSize := len(pcmData)
	riffSize := 4 + (8 + fmtSize) + (8 + dataSize) // "WAVE" + fmt chunk + data chunk

	var buf bytes.Buffer
	// RIFF header
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(riffSize))
	buf.WriteString("WAVE")
	// fmt chunk
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(fmtSize))
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(channels))
	binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(&buf, binary.LittleEndian, uint32(byteRate))
	binary.Write(&buf, binary.LittleEndian, uint16(blockAlign))
	binary.Write(&buf, binary.LittleEndian, uint16(bitsPerSample))
	// data chunk
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(dataSize))
	buf.Write(pcmData)

	return buf.Bytes()
}

func writeTempFile(t *testing.T, data []byte) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "slice-test-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestSliceMP3_CBR(t *testing.T) {
	// 1000 frames at 44100 Hz, 1152 samples/frame ≈ 26.12 seconds
	data := buildCBRMP3(t, 1000)
	f := writeTempFile(t, data)

	w := httptest.NewRecorder()
	ok := sliceMP3(w, f, int64(len(data)), "audio/mpeg", 5, 10)
	if !ok {
		t.Fatal("sliceMP3 returned false for valid CBR file")
	}

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Fatal("empty response body")
	}

	// Content-Length should match body
	cl := resp.Header.Get("Content-Length")
	if cl == "" {
		t.Fatal("missing Content-Length")
	}

	// Slice should be smaller than the original
	if len(body) >= len(data) {
		t.Errorf("slice (%d bytes) not smaller than original (%d bytes)", len(body), len(data))
	}

	// Rough check: 10 seconds of 128kbps ≈ 160KB, allow 50% tolerance
	expected := 128 * 1000 / 8 * 10 // 160000 bytes
	if len(body) < expected/2 || len(body) > expected*2 {
		t.Errorf("slice size %d not in expected range [%d, %d]", len(body), expected/2, expected*2)
	}
}

func TestSliceMP3_VBR(t *testing.T) {
	data := buildVBRMP3(t, 1000)
	f := writeTempFile(t, data)

	w := httptest.NewRecorder()
	ok := sliceMP3(w, f, int64(len(data)), "audio/mpeg", 5, 10)
	if !ok {
		t.Fatal("sliceMP3 returned false for valid VBR file")
	}

	body, _ := io.ReadAll(w.Result().Body)
	if len(body) == 0 {
		t.Fatal("empty response body")
	}
	if len(body) >= len(data) {
		t.Errorf("slice (%d bytes) not smaller than original (%d bytes)", len(body), len(data))
	}
}

func TestSliceMP3_StartBeyondDuration(t *testing.T) {
	data := buildCBRMP3(t, 100) // ~2.6 seconds
	f := writeTempFile(t, data)

	w := httptest.NewRecorder()
	ok := sliceMP3(w, f, int64(len(data)), "audio/mpeg", 100, 10)
	if ok {
		t.Fatal("sliceMP3 should return false when start exceeds duration")
	}
}

func TestSliceMP3_WithID3(t *testing.T) {
	// Prepend an ID3v2 tag
	id3Size := 512
	var id3 bytes.Buffer
	id3.WriteString("ID3")
	id3.WriteByte(3) // version 2.3
	id3.WriteByte(0) // revision
	id3.WriteByte(0) // flags
	// Syncsafe size: 512 - 10 = 502 → encode as syncsafe
	tagSize := id3Size - 10
	id3.WriteByte(byte(tagSize >> 21 & 0x7F))
	id3.WriteByte(byte(tagSize >> 14 & 0x7F))
	id3.WriteByte(byte(tagSize >> 7 & 0x7F))
	id3.WriteByte(byte(tagSize & 0x7F))
	id3.Write(make([]byte, tagSize)) // padding

	mp3 := buildCBRMP3(t, 1000)
	data := append(id3.Bytes(), mp3...)
	f := writeTempFile(t, data)

	w := httptest.NewRecorder()
	ok := sliceMP3(w, f, int64(len(data)), "audio/mpeg", 5, 10)
	if !ok {
		t.Fatal("sliceMP3 should handle ID3v2 tagged file")
	}

	body, _ := io.ReadAll(w.Result().Body)
	if len(body) >= len(data) {
		t.Errorf("slice (%d bytes) not smaller than original (%d bytes)", len(body), len(data))
	}
}

func TestSliceFLAC(t *testing.T) {
	sampleRate := 44100
	totalSamples := int64(44100 * 30) // 30 seconds
	audioData := make([]byte, 100000) // fake audio

	seekPoints := []flacSeekPoint{
		{sampleNumber: 0, byteOffset: 0, numSamples: 4096},
		{sampleNumber: 44100 * 5, byteOffset: 16000, numSamples: 4096},
		{sampleNumber: 44100 * 10, byteOffset: 33000, numSamples: 4096},
		{sampleNumber: 44100 * 15, byteOffset: 50000, numSamples: 4096},
		{sampleNumber: 44100 * 20, byteOffset: 66000, numSamples: 4096},
		{sampleNumber: 44100 * 25, byteOffset: 83000, numSamples: 4096},
	}

	data := buildTestFLAC(t, sampleRate, totalSamples, seekPoints, audioData)
	f := writeTempFile(t, data)

	w := httptest.NewRecorder()
	ok := sliceFLAC(w, f, int64(len(data)), "audio/flac", 5, 10)
	if !ok {
		t.Fatal("sliceFLAC returned false")
	}

	body, _ := io.ReadAll(w.Result().Body)
	// Should contain fLaC marker + STREAMINFO + partial audio
	if len(body) < 42 { // 4 + 4 + 34 = minimum
		t.Fatalf("response too small: %d bytes", len(body))
	}
	if string(body[0:4]) != "fLaC" {
		t.Error("missing fLaC marker in response")
	}
	// Should be smaller than original (we asked for 10s of 30s)
	if len(body) >= len(data) {
		t.Errorf("slice (%d) not smaller than original (%d)", len(body), len(data))
	}
}

func TestSliceFLAC_NoSeektable(t *testing.T) {
	data := buildTestFLAC(t, 44100, 44100*30, nil, make([]byte, 1000))
	f := writeTempFile(t, data)

	w := httptest.NewRecorder()
	ok := sliceFLAC(w, f, int64(len(data)), "audio/flac", 5, 10)
	if ok {
		t.Fatal("sliceFLAC should return false without SEEKTABLE")
	}
}

func TestSliceWAV(t *testing.T) {
	// 10 seconds of 44100 Hz, 16-bit mono = 882000 bytes of PCM
	sampleRate := 44100
	channels := 1
	bps := 16
	seconds := 10
	pcmSize := sampleRate * channels * (bps / 8) * seconds
	pcm := make([]byte, pcmSize)
	// Fill with a pattern so we can verify the slice position
	for i := range pcm {
		pcm[i] = byte(i % 256)
	}

	data := buildTestWAV(t, sampleRate, channels, bps, pcm)
	f := writeTempFile(t, data)

	w := httptest.NewRecorder()
	ok := sliceWAV(w, f, int64(len(data)), "audio/wav", 2, 3)
	if !ok {
		t.Fatal("sliceWAV returned false")
	}

	body, _ := io.ReadAll(w.Result().Body)
	if string(body[0:4]) != "RIFF" {
		t.Error("missing RIFF header in response")
	}
	if string(body[8:12]) != "WAVE" {
		t.Error("missing WAVE format in response")
	}

	// Expected data size: 3 seconds * 44100 * 1 * 2 = 264600 bytes
	expectedData := 3 * sampleRate * channels * (bps / 8)
	// Total = 12 + 8 + 16 + 8 + dataSize = 44 + dataSize
	expectedTotal := 44 + expectedData
	if len(body) != expectedTotal {
		t.Errorf("response size = %d, want %d", len(body), expectedTotal)
	}
}

func TestSliceWAV_ClampToDuration(t *testing.T) {
	pcm := make([]byte, 44100*2*2) // 1 second of 44100 Hz, 16-bit stereo
	data := buildTestWAV(t, 44100, 2, 16, pcm)
	f := writeTempFile(t, data)

	w := httptest.NewRecorder()
	ok := sliceWAV(w, f, int64(len(data)), "audio/wav", 0.5, 10) // ask for 10s of 1s file
	if !ok {
		t.Fatal("sliceWAV returned false")
	}

	body, _ := io.ReadAll(w.Result().Body)
	// Should get 0.5 seconds (clamped to remaining duration)
	expectedData := int(0.5 * 44100) * 4 // 0.5s * 44100 * 2ch * 2bytes
	expectedTotal := 44 + expectedData
	if len(body) != expectedTotal {
		t.Errorf("response size = %d, want %d", len(body), expectedTotal)
	}
}

// TestSliceViaServer tests the full handleStream path with slice params.
func TestSliceViaServer(t *testing.T) {
	srv := testServer(t)
	conn, put, err := srv.db.WriteConn()
	if err != nil {
		t.Fatal(err)
	}

	// Create a real MP3 file in the temp dir
	mp3Data := buildCBRMP3(t, 500) // ~13 seconds
	mp3Path := srv.cfg.MusicDir + "/test.mp3"
	if err := os.WriteFile(mp3Path, mp3Data, 0o644); err != nil {
		t.Fatal(err)
	}

	err = sqlitex.ExecuteTransient(conn,
		`INSERT INTO artist (id, name) VALUES ('a1', 'Test')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = sqlitex.ExecuteTransient(conn,
		`INSERT INTO album (id, artist_id, name, song_count, duration) VALUES ('al1', 'a1', 'Test', 1, 13)`, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = sqlitex.ExecuteTransient(conn,
		`INSERT INTO song (id, album_id, artist_id, title, track, duration, size, suffix, bitrate, content_type, path)
		 VALUES ('s1', 'al1', 'a1', 'Test', 1, 13, ?, 'mp3', 128, 'audio/mpeg', ?)`,
		&sqlitex.ExecOptions{Args: []any{len(mp3Data), mp3Path}})
	if err != nil {
		t.Fatal(err)
	}
	put()

	t.Run("with slice params", func(t *testing.T) {
		w := get(t, srv, "/rest/stream?id=s1&startTime=2&duration=5")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); ct != "audio/mpeg" {
			t.Errorf("Content-Type = %q", ct)
		}
		if w.Body.Len() >= len(mp3Data) {
			t.Error("sliced response not smaller than original")
		}
	})

	t.Run("without slice params", func(t *testing.T) {
		w := get(t, srv, "/rest/stream?id=s1")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		if w.Body.Len() != len(mp3Data) {
			t.Errorf("full stream size = %d, want %d", w.Body.Len(), len(mp3Data))
		}
	})

	t.Run("invalid slice params fall through", func(t *testing.T) {
		w := get(t, srv, "/rest/stream?id=s1&startTime=-1&duration=5")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		// Negative startTime should fall through to full file
		if w.Body.Len() != len(mp3Data) {
			t.Errorf("expected full file, got %d bytes", w.Body.Len())
		}
	})
}
