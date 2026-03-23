package scanner

import (
	"encoding/binary"
	"io"
	"strconv"
	"strings"
	"unicode/utf16"
)

// id3Tags holds the text metadata extracted from an ID3v2 tag.
type id3Tags struct {
	title   string
	artist  string
	album   string
	genre   string
	year    int
	track   int
	disc    int
	tagSize int64 // total tag size including header (offset where audio begins)
}

// id3ReadCap is the maximum bytes to read from the start of a file for ID3v2
// parsing: 10-byte header + 4KB tag body. 4KB covers all text frames in
// practice — APIC (album art) is always placed after text frames by
// real-world taggers, and text metadata rarely exceeds 2KB.
const id3ReadCap = 10 + 4096

// readID3v2 reads only text frames from an ID3v2 tag. Performs a single read
// of min(10+tagSize, 4106) bytes to get both the header and tag body in one
// syscall, then parses frames from memory. Returns false if no valid ID3v2
// header.
func readID3v2(r io.Reader) (id3Tags, bool) {
	// Single read: header + tag body in one syscall.
	var buf [id3ReadCap]byte
	n, _ := io.ReadAtLeast(r, buf[:], 10)
	if n < 10 {
		return id3Tags{}, false
	}

	if buf[0] != 'I' || buf[1] != 'D' || buf[2] != '3' {
		return id3Tags{}, false
	}

	major := buf[3] // 2, 3, or 4
	if major < 2 || major > 4 {
		return id3Tags{}, false
	}

	// Syncsafe integer: 4 × 7 bits.
	rawTagSize := int64(buf[6])<<21 | int64(buf[7])<<14 | int64(buf[8])<<7 | int64(buf[9])

	body := buf[10:n]
	if int64(len(body)) > rawTagSize {
		body = body[:rawTagSize]
	}

	tags := parseID3v2Frames(body, major, rawTagSize, buf[5])
	tags.tagSize = 10 + rawTagSize
	return tags, true
}

// parseID3v2Frames parses ID3v2 frames from buf (tag body after the 10-byte
// header). rawTagSize is the declared tag size from the header; flags is byte 5
// of the header. Operates entirely in memory — no I/O.
func parseID3v2Frames(buf []byte, major byte, rawTagSize int64, flags byte) id3Tags {
	var tags id3Tags
	pos := 0

	// Skip extended header if present (flag bit 6).
	if flags&0x40 != 0 && major >= 3 && len(buf) >= 4 {
		extSize := int64(binary.BigEndian.Uint32(buf[:4]))
		if major == 4 {
			extSize = int64(buf[0])<<21 | int64(buf[1])<<14 | int64(buf[2])<<7 | int64(buf[3])
			extSize -= 4
		}
		pos = 4 + int(extSize)
	}

	// Frame header sizes differ between v2.2 (6 bytes) and v2.3/v2.4 (10 bytes).
	frameIDLen := 4
	frameSizeLen := 4
	frameFlagsLen := 2
	if major == 2 {
		frameIDLen = 3
		frameSizeLen = 3
		frameFlagsLen = 0
	}
	frameHdrLen := frameIDLen + frameSizeLen + frameFlagsLen

	for {
		if int64(pos) >= rawTagSize {
			break
		}
		if pos+frameHdrLen > len(buf) {
			break // hit read cap
		}

		hdrBuf := buf[pos : pos+frameHdrLen]
		pos += frameHdrLen

		// Padding (all zeros) means end of frames.
		if hdrBuf[0] == 0 {
			break
		}

		frameID := string(hdrBuf[:frameIDLen])

		var frameSize int64
		if major == 2 {
			frameSize = int64(hdrBuf[3])<<16 | int64(hdrBuf[4])<<8 | int64(hdrBuf[5])
		} else if major == 4 {
			frameSize = int64(hdrBuf[4])<<21 | int64(hdrBuf[5])<<14 | int64(hdrBuf[6])<<7 | int64(hdrBuf[7])
		} else {
			frameSize = int64(binary.BigEndian.Uint32(hdrBuf[frameIDLen : frameIDLen+4]))
		}

		if frameSize <= 0 {
			break
		}

		end := pos + int(frameSize)

		// Only decode text frames we care about.
		if wantTextFrame(frameID, major) && frameSize < 4096 {
			if end > len(buf) {
				break // frame extends past read cap
			}
			val := decodeTextFrame(buf[pos:end])
			applyTag(&tags, frameID, val, major)
		}

		// Advance past frame data (or skip unwanted frames like APIC).
		if end > len(buf) {
			break // can't advance past read cap
		}
		pos = end
	}

	return tags
}

// wantTextFrame returns true for frame IDs we need to extract.
func wantTextFrame(id string, major byte) bool {
	switch id {
	// v2.3/v2.4 frame IDs
	case "TIT2", "TPE1", "TALB", "TCON", "TRCK", "TPOS", "TDRC", "TYER":
		return true
	// v2.2 frame IDs
	case "TT2", "TP1", "TAL", "TCO", "TRK", "TPA", "TYE":
		return major == 2
	}
	return false
}

// applyTag sets the appropriate field on tags based on frame ID.
func applyTag(tags *id3Tags, id, val string, major byte) {
	if val == "" {
		return
	}
	switch id {
	case "TIT2", "TT2":
		tags.title = val
	case "TPE1", "TP1":
		tags.artist = val
	case "TALB", "TAL":
		tags.album = val
	case "TCON", "TCO":
		tags.genre = cleanGenre(val)
	case "TRCK", "TRK":
		tags.track = parseTrackDisc(val)
	case "TPOS", "TPA":
		tags.disc = parseTrackDisc(val)
	case "TDRC", "TYER", "TYE":
		if len(val) >= 4 {
			tags.year, _ = strconv.Atoi(val[:4])
		}
	}
}

// decodeTextFrame decodes an ID3v2 text frame payload.
// First byte is encoding: 0=ISO-8859-1, 1=UTF-16 BOM, 2=UTF-16BE, 3=UTF-8.
func decodeTextFrame(data []byte) string {
	if len(data) < 2 {
		return ""
	}
	encoding := data[0]
	payload := data[1:]

	switch encoding {
	case 0: // ISO-8859-1
		return trimNull(string(payload))
	case 3: // UTF-8
		return trimNull(string(payload))
	case 1: // UTF-16 with BOM
		return trimNull(decodeUTF16(payload))
	case 2: // UTF-16BE
		return trimNull(decodeUTF16BE(payload))
	}
	return trimNull(string(payload))
}

func decodeUTF16(data []byte) string {
	if len(data) < 2 {
		return ""
	}
	bigEndian := true
	if data[0] == 0xFF && data[1] == 0xFE {
		bigEndian = false
		data = data[2:]
	} else if data[0] == 0xFE && data[1] == 0xFF {
		data = data[2:]
	}
	if bigEndian {
		return decodeUTF16BE(data)
	}
	return decodeUTF16LE(data)
}

func decodeUTF16BE(data []byte) string {
	if len(data)%2 != 0 {
		data = data[:len(data)-1]
	}
	u16 := make([]uint16, len(data)/2)
	for i := range u16 {
		u16[i] = binary.BigEndian.Uint16(data[i*2:])
	}
	return string(utf16.Decode(u16))
}

func decodeUTF16LE(data []byte) string {
	if len(data)%2 != 0 {
		data = data[:len(data)-1]
	}
	u16 := make([]uint16, len(data)/2)
	for i := range u16 {
		u16[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	return string(utf16.Decode(u16))
}

func trimNull(s string) string {
	return strings.TrimRight(s, "\x00")
}

// parseTrackDisc extracts the number from "N" or "N/M" format.
func parseTrackDisc(s string) int {
	if idx := strings.IndexByte(s, '/'); idx >= 0 {
		s = s[:idx]
	}
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// cleanGenre strips ID3v1 numeric genre references like "(17)" or "(17)Rock".
func cleanGenre(s string) string {
	if len(s) == 0 || s[0] != '(' {
		return s
	}
	if idx := strings.IndexByte(s, ')'); idx >= 0 {
		after := s[idx+1:]
		if after != "" {
			return after
		}
		return s[1:idx]
	}
	return s
}
