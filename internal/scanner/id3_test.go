package scanner

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// buildID3v2Tag constructs a minimal ID3v2.3 tag with the given text frames.
func buildID3v2Tag(frames map[string]string) []byte {
	var frameBuf bytes.Buffer

	for id, val := range frames {
		// Text frame: 4-byte ID, 4-byte size, 2-byte flags, 1-byte encoding (UTF-8=3), text.
		payload := append([]byte{3}, []byte(val)...) // encoding 3 = UTF-8
		frameBuf.WriteString(id)
		var sizeBuf [4]byte
		binary.BigEndian.PutUint32(sizeBuf[:], uint32(len(payload)))
		frameBuf.Write(sizeBuf[:])
		frameBuf.Write([]byte{0, 0}) // flags
		frameBuf.Write(payload)
	}

	frameData := frameBuf.Bytes()

	// ID3v2 header: "ID3", version 3.0, no flags, syncsafe size.
	var hdr [10]byte
	copy(hdr[:3], "ID3")
	hdr[3] = 3 // major version
	hdr[4] = 0 // revision
	hdr[5] = 0 // flags

	size := len(frameData)
	hdr[6] = byte((size >> 21) & 0x7F)
	hdr[7] = byte((size >> 14) & 0x7F)
	hdr[8] = byte((size >> 7) & 0x7F)
	hdr[9] = byte(size & 0x7F)

	return append(hdr[:], frameData...)
}

func TestReadID3v2Basic(t *testing.T) {
	data := buildID3v2Tag(map[string]string{
		"TIT2": "Dancing Queen",
		"TPE1": "ABBA",
		"TALB": "Gold",
		"TCON": "Pop",
		"TRCK": "3/19",
		"TPOS": "1/1",
		"TDRC": "1976",
	})

	r := bytes.NewReader(data)
	tags, ok := readID3v2(r)
	if !ok {
		t.Fatal("readID3v2 returned false")
	}

	checks := []struct {
		name string
		got  string
		want string
	}{
		{"title", tags.title, "Dancing Queen"},
		{"artist", tags.artist, "ABBA"},
		{"album", tags.album, "Gold"},
		{"genre", tags.genre, "Pop"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if tags.track != 3 {
		t.Errorf("track = %d, want 3", tags.track)
	}
	if tags.disc != 1 {
		t.Errorf("disc = %d, want 1", tags.disc)
	}
	if tags.year != 1976 {
		t.Errorf("year = %d, want 1976", tags.year)
	}
}

func TestReadID3v2SkipsAPIC(t *testing.T) {
	// Build a tag with a text frame, then a large APIC frame, then another text frame.
	var buf bytes.Buffer

	// TIT2 frame
	title := append([]byte{3}, []byte("Test Title")...)
	buf.WriteString("TIT2")
	var sizeBuf [4]byte
	binary.BigEndian.PutUint32(sizeBuf[:], uint32(len(title)))
	buf.Write(sizeBuf[:])
	buf.Write([]byte{0, 0})
	buf.Write(title)

	// APIC frame (fake 1KB of art data — should be seeked past, not read)
	apicData := make([]byte, 1024)
	buf.WriteString("APIC")
	binary.BigEndian.PutUint32(sizeBuf[:], uint32(len(apicData)))
	buf.Write(sizeBuf[:])
	buf.Write([]byte{0, 0})
	buf.Write(apicData)

	// TPE1 frame (after APIC)
	artist := append([]byte{3}, []byte("Test Artist")...)
	buf.WriteString("TPE1")
	binary.BigEndian.PutUint32(sizeBuf[:], uint32(len(artist)))
	buf.Write(sizeBuf[:])
	buf.Write([]byte{0, 0})
	buf.Write(artist)

	frameData := buf.Bytes()

	// Build header.
	var hdr [10]byte
	copy(hdr[:3], "ID3")
	hdr[3] = 3
	size := len(frameData)
	hdr[6] = byte((size >> 21) & 0x7F)
	hdr[7] = byte((size >> 14) & 0x7F)
	hdr[8] = byte((size >> 7) & 0x7F)
	hdr[9] = byte(size & 0x7F)

	data := append(hdr[:], frameData...)
	r := bytes.NewReader(data)
	tags, ok := readID3v2(r)
	if !ok {
		t.Fatal("readID3v2 returned false for tag with APIC")
	}
	if tags.title != "Test Title" {
		t.Errorf("title = %q, want %q", tags.title, "Test Title")
	}
	if tags.artist != "Test Artist" {
		t.Errorf("artist = %q, want %q (frame after APIC should be read)", tags.artist, "Test Artist")
	}
}

func TestReadID3v2OversizedAPIC(t *testing.T) {
	// Tag: TIT2 frame, then APIC >4KB, then TPE1 after the cap.
	// We expect TIT2 to parse, APIC to cause a clean stop, TPE1 lost.
	var buf bytes.Buffer

	// TIT2 frame
	title := append([]byte{3}, []byte("Before Art")...)
	buf.WriteString("TIT2")
	var sizeBuf [4]byte
	binary.BigEndian.PutUint32(sizeBuf[:], uint32(len(title)))
	buf.Write(sizeBuf[:])
	buf.Write([]byte{0, 0})
	buf.Write(title)

	// APIC frame: 8KB of fake image data (exceeds 4KB read cap)
	apicData := make([]byte, 8*1024)
	buf.WriteString("APIC")
	binary.BigEndian.PutUint32(sizeBuf[:], uint32(len(apicData)))
	buf.Write(sizeBuf[:])
	buf.Write([]byte{0, 0})
	buf.Write(apicData)

	// TPE1 frame placed after the oversized APIC
	artist := append([]byte{3}, []byte("After Art")...)
	buf.WriteString("TPE1")
	binary.BigEndian.PutUint32(sizeBuf[:], uint32(len(artist)))
	buf.Write(sizeBuf[:])
	buf.Write([]byte{0, 0})
	buf.Write(artist)

	frameData := buf.Bytes()

	var hdr [10]byte
	copy(hdr[:3], "ID3")
	hdr[3] = 3
	size := len(frameData)
	hdr[6] = byte((size >> 21) & 0x7F)
	hdr[7] = byte((size >> 14) & 0x7F)
	hdr[8] = byte((size >> 7) & 0x7F)
	hdr[9] = byte(size & 0x7F)

	data := append(hdr[:], frameData...)
	r := bytes.NewReader(data)
	tags, ok := readID3v2(r)
	if !ok {
		t.Fatal("readID3v2 returned false for tag with oversized APIC")
	}
	if tags.title != "Before Art" {
		t.Errorf("title = %q, want %q (should parse frame before APIC)", tags.title, "Before Art")
	}
	// TPE1 is beyond the 4KB read cap — graceful degradation means it's lost.
	if tags.artist != "" {
		t.Errorf("artist = %q, want empty (frame after 4KB cap should be lost)", tags.artist)
	}
	// tagSize must reflect the full declared size, not the read cap.
	wantTagSize := int64(10 + size)
	if tags.tagSize != wantTagSize {
		t.Errorf("tagSize = %d, want %d", tags.tagSize, wantTagSize)
	}
}

func TestReadID3v2NotID3(t *testing.T) {
	r := bytes.NewReader([]byte("not an ID3 tag at all"))
	_, ok := readID3v2(r)
	if ok {
		t.Error("readID3v2 should return false for non-ID3 data")
	}
}

func TestCleanGenre(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Rock", "Rock"},
		{"(17)Rock", "Rock"},
		{"(17)", "17"},
		{"", ""},
		{"(255)Custom", "Custom"},
	}
	for _, tt := range tests {
		got := cleanGenre(tt.in)
		if got != tt.want {
			t.Errorf("cleanGenre(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseTrackDisc(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"3", 3},
		{"3/19", 3},
		{"1/1", 1},
		{"", 0},
		{"0", 0},
	}
	for _, tt := range tests {
		got := parseTrackDisc(tt.in)
		if got != tt.want {
			t.Errorf("parseTrackDisc(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
