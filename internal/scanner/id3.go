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

// lyricsReadCap is the maximum tag size we'll read for lyrics extraction.
// Lyrics frames (USLT/SYLT) can be 10-50KB+, so we need more than the
// scanner's 4KB cap. 512KB covers any reasonable lyrics payload.
const lyricsReadCap = 512 * 1024

// LyricLine represents a single line in a lyrics frame.
type LyricLine struct {
	Start int    // milliseconds; -1 for unsynced
	Value string
}

// LyricFrame represents a parsed USLT or SYLT frame.
type LyricFrame struct {
	Lang   string
	Synced bool
	Lines  []LyricLine
}

// ReadID3v2Lyrics reads USLT and SYLT frames from an ID3v2 tag. Unlike
// readID3v2 (optimized for the scanner with a 4KB cap), this reads the full
// tag body (up to 512KB) to find lyrics frames which can be large.
func ReadID3v2Lyrics(r io.ReadSeeker) ([]LyricFrame, error) {
	var hdr [10]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	if hdr[0] != 'I' || hdr[1] != 'D' || hdr[2] != '3' {
		return nil, nil // not ID3v2, no lyrics
	}

	major := hdr[3]
	if major < 2 || major > 4 {
		return nil, nil
	}

	rawTagSize := int64(hdr[6])<<21 | int64(hdr[7])<<14 | int64(hdr[8])<<7 | int64(hdr[9])
	readSize := rawTagSize
	if readSize > lyricsReadCap {
		readSize = lyricsReadCap
	}

	body := make([]byte, readSize)
	n, _ := io.ReadFull(r, body)
	body = body[:n]

	return parseLyricsFrames(body, major, rawTagSize, hdr[5]), nil
}

// parseLyricsFrames scans ID3v2 frames looking for USLT/SYLT (or v2.2 ULT/SLT).
func parseLyricsFrames(buf []byte, major byte, rawTagSize int64, flags byte) []LyricFrame {
	pos := 0

	// Skip extended header if present.
	if flags&0x40 != 0 && major >= 3 && len(buf) >= 4 {
		extSize := int64(binary.BigEndian.Uint32(buf[:4]))
		if major == 4 {
			extSize = int64(buf[0])<<21 | int64(buf[1])<<14 | int64(buf[2])<<7 | int64(buf[3])
			extSize -= 4
		}
		pos = 4 + int(extSize)
	}

	frameIDLen := 4
	frameSizeLen := 4
	frameFlagsLen := 2
	if major == 2 {
		frameIDLen = 3
		frameSizeLen = 3
		frameFlagsLen = 0
	}
	frameHdrLen := frameIDLen + frameSizeLen + frameFlagsLen

	var frames []LyricFrame

	for {
		if int64(pos) >= rawTagSize {
			break
		}
		if pos+frameHdrLen > len(buf) {
			break
		}

		hdrBuf := buf[pos : pos+frameHdrLen]
		pos += frameHdrLen

		if hdrBuf[0] == 0 {
			break // padding
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
		if end > len(buf) {
			break
		}

		data := buf[pos:end]

		switch frameID {
		case "USLT", "ULT":
			if f, ok := parseUSLT(data); ok {
				frames = append(frames, f)
			}
		case "SYLT", "SLT":
			if f, ok := parseSYLT(data); ok {
				frames = append(frames, f)
			}
		}

		pos = end
	}

	return frames
}

// parseUSLT parses an unsynced lyrics frame (USLT/ULT).
// Format: encoding(1) + lang(3) + null-terminated descriptor + lyrics text
func parseUSLT(data []byte) (LyricFrame, bool) {
	if len(data) < 5 { // encoding + lang(3) + at least 1 byte
		return LyricFrame{}, false
	}
	encoding := data[0]
	lang := string(data[1:4])
	rest := data[4:]

	// Skip null-terminated content descriptor
	rest = skipNullTerminated(rest, encoding)

	text := decodeByEncoding(rest, encoding)
	text = trimNull(text)

	if text == "" {
		return LyricFrame{}, false
	}

	// Split into lines
	lines := splitLyricLines(text)

	return LyricFrame{
		Lang:   lang,
		Synced: false,
		Lines:  lines,
	}, true
}

// parseSYLT parses a synced lyrics frame (SYLT/SLT).
// Format: encoding(1) + lang(3) + timestampFormat(1) + contentType(1) +
// null-terminated descriptor + then repeating [text\0 timestamp(4)]
func parseSYLT(data []byte) (LyricFrame, bool) {
	if len(data) < 7 { // encoding + lang(3) + tsFormat(1) + contentType(1) + at least 1
		return LyricFrame{}, false
	}
	encoding := data[0]
	lang := string(data[1:4])
	tsFormat := data[4] // 1=mpeg frames, 2=milliseconds
	// contentType at data[5]: 0=other, 1=lyrics, etc. — we accept any
	_ = data[5]
	rest := data[6:]

	// Skip null-terminated content descriptor
	rest = skipNullTerminated(rest, encoding)

	var lines []LyricLine
	for len(rest) > 0 {
		// Find null terminator for the text segment
		text, remainder, ok := readNullTerminated(rest, encoding)
		if !ok {
			break
		}

		if len(remainder) < 4 {
			break
		}
		ts := int(binary.BigEndian.Uint32(remainder[:4]))
		remainder = remainder[4:]

		// Convert MPEG frames to milliseconds if needed (assume ~26ms per frame for MPEG1 Layer3)
		ms := ts
		if tsFormat == 1 {
			ms = ts * 26
		}

		lines = append(lines, LyricLine{Start: ms, Value: trimNull(text)})
		rest = remainder
	}

	if len(lines) == 0 {
		return LyricFrame{}, false
	}

	return LyricFrame{
		Lang:   lang,
		Synced: true,
		Lines:  lines,
	}, true
}

// skipNullTerminated advances past a null-terminated string for the given encoding.
func skipNullTerminated(data []byte, encoding byte) []byte {
	if encoding == 1 || encoding == 2 {
		// UTF-16: look for double-null (0x00 0x00) on even boundary
		for i := 0; i+1 < len(data); i += 2 {
			if data[i] == 0 && data[i+1] == 0 {
				return data[i+2:]
			}
		}
		return nil
	}
	// ISO-8859-1 or UTF-8: look for single null
	for i, b := range data {
		if b == 0 {
			return data[i+1:]
		}
	}
	return nil
}

// readNullTerminated reads a null-terminated string and returns the decoded text,
// the remaining data after the null terminator, and whether a terminator was found.
func readNullTerminated(data []byte, encoding byte) (string, []byte, bool) {
	if encoding == 1 || encoding == 2 {
		for i := 0; i+1 < len(data); i += 2 {
			if data[i] == 0 && data[i+1] == 0 {
				text := decodeByEncoding(data[:i], encoding)
				return text, data[i+2:], true
			}
		}
		return "", nil, false
	}
	for i, b := range data {
		if b == 0 {
			return string(data[:i]), data[i+1:], true
		}
	}
	return "", nil, false
}

// decodeByEncoding decodes bytes using the ID3v2 text encoding byte.
func decodeByEncoding(data []byte, encoding byte) string {
	switch encoding {
	case 0: // ISO-8859-1
		return string(data)
	case 3: // UTF-8
		return string(data)
	case 1: // UTF-16 with BOM
		return decodeUTF16(data)
	case 2: // UTF-16BE
		return decodeUTF16BE(data)
	}
	return string(data)
}

// splitLyricLines splits unsynced lyrics text into individual lines.
func splitLyricLines(text string) []LyricLine {
	raw := strings.Split(text, "\n")
	lines := make([]LyricLine, 0, len(raw))
	for _, l := range raw {
		l = strings.TrimRight(l, "\r")
		lines = append(lines, LyricLine{Start: -1, Value: l})
	}
	return lines
}

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
