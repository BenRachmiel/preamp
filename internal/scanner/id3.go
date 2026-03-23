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

// readID3v2 reads only text frames from an ID3v2 tag, skipping binary
// frames (APIC, etc.) without allocating memory for them. Works with any
// io.Reader (including bufio.Reader) — no seeking required.
// Returns false if the data does not have a valid ID3v2 header.
func readID3v2(r io.Reader) (id3Tags, bool) {
	// Read 10-byte ID3v2 header.
	var hdr [10]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return id3Tags{}, false
	}
	if hdr[0] != 'I' || hdr[1] != 'D' || hdr[2] != '3' {
		return id3Tags{}, false
	}

	major := hdr[3] // 2, 3, or 4
	if major < 2 || major > 4 {
		return id3Tags{}, false
	}

	// Syncsafe integer: 4 × 7 bits.
	rawTagSize := int64(hdr[6])<<21 | int64(hdr[7])<<14 | int64(hdr[8])<<7 | int64(hdr[9])

	var tags id3Tags
	tags.tagSize = 10 + rawTagSize

	// Wrap in a LimitedReader so we stop at the tag boundary.
	lr := &io.LimitedReader{R: r, N: rawTagSize}

	// Skip extended header if present (flag bit 6).
	if hdr[5]&0x40 != 0 && major >= 3 {
		var extHdr [4]byte
		if _, err := io.ReadFull(lr, extHdr[:]); err != nil {
			return tags, true // partial is fine, we got tagSize
		}
		extSize := int64(binary.BigEndian.Uint32(extHdr[:]))
		if major == 4 {
			extSize = int64(extHdr[0])<<21 | int64(extHdr[1])<<14 | int64(extHdr[2])<<7 | int64(extHdr[3])
			extSize -= 4
		}
		if _, err := io.CopyN(io.Discard, lr, extSize); err != nil {
			return tags, true
		}
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

	hdrBuf := make([]byte, frameHdrLen)

	for lr.N > 0 {
		if _, err := io.ReadFull(lr, hdrBuf); err != nil {
			break
		}

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

		if frameSize <= 0 || frameSize > lr.N {
			break
		}

		// Only read text frames we care about.
		if wantTextFrame(frameID, major) && frameSize < 4096 {
			data := make([]byte, frameSize)
			if _, err := io.ReadFull(lr, data); err != nil {
				break
			}
			val := decodeTextFrame(data)
			applyTag(&tags, frameID, val, major)
		} else {
			// Skip unwanted frames (APIC, COMM, etc.) without reading into memory.
			if _, err := io.CopyN(io.Discard, lr, frameSize); err != nil {
				break
			}
		}
	}

	return tags, true
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
	// Detect BOM.
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
