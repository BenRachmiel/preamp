package scanner

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"sync"
)

// parseDuration returns accurate duration (seconds) and bitrate (kbps) for an audio file.
// It tries native parsing for MP3/FLAC first, then falls back to ffprobe.
func parseDuration(path, ext string, log *slog.Logger) (durationSecs, bitrateKbps int) {
	switch ext {
	case ".mp3":
		d, br, err := parseMP3Duration(path)
		if err == nil && d > 0 {
			return d, br
		}
	case ".flac":
		d, br, err := parseFLACDuration(path)
		if err == nil && d > 0 {
			return d, br
		}
	}

	// Fallback: ffprobe
	d, br, err := ffprobeDuration(path, log)
	if err == nil && d > 0 {
		return d, br
	}

	return 0, 0
}

// MP3 frame header bitrate tables (MPEG1 Layer III)
var mp3BitrateTable = [16]int{
	0, 32, 40, 48, 56, 64, 80, 96,
	112, 128, 160, 192, 224, 256, 320, 0,
}

var mp3SampleRateTable = [4]int{44100, 48000, 32000, 0}

// parseMP3Duration parses MP3 duration from Xing/VBRI VBR header, or first-frame CBR estimation.
func parseMP3Duration(path string) (durationSecs, bitrateKbps int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	// Skip ID3v2 tag if present.
	var header [10]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return 0, 0, err
	}
	offset := int64(0)
	if header[0] == 'I' && header[1] == 'D' && header[2] == '3' {
		// ID3v2 size is a syncsafe integer in bytes 6-9.
		tagSize := int64(header[6])<<21 | int64(header[7])<<14 | int64(header[8])<<7 | int64(header[9])
		offset = tagSize + 10
	}

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, 0, err
	}

	// Find first valid MPEG frame sync.
	var syncBuf [4]byte
	for i := 0; i < 65536; i++ {
		if _, err := io.ReadFull(f, syncBuf[:1]); err != nil {
			return 0, 0, err
		}
		if syncBuf[0] != 0xFF {
			continue
		}
		if _, err := io.ReadFull(f, syncBuf[1:4]); err != nil {
			return 0, 0, err
		}
		if syncBuf[1]&0xE0 != 0xE0 {
			// Not a sync, back up 3 bytes.
			if _, err := f.Seek(-3, io.SeekCurrent); err != nil {
				return 0, 0, err
			}
			continue
		}

		// Parse frame header.
		version := (syncBuf[1] >> 3) & 0x03     // 11=MPEG1, 10=MPEG2, 00=MPEG2.5
		layer := (syncBuf[1] >> 1) & 0x03        // 01=Layer III
		bitrateIdx := (syncBuf[2] >> 4) & 0x0F
		sampleRateIdx := (syncBuf[2] >> 2) & 0x03
		padding := (syncBuf[2] >> 1) & 0x01

		if version != 3 || layer != 1 { // only MPEG1 Layer III for now
			if _, err := f.Seek(-3, io.SeekCurrent); err != nil {
				return 0, 0, err
			}
			continue
		}
		if bitrateIdx == 0 || bitrateIdx == 15 || sampleRateIdx == 3 {
			if _, err := f.Seek(-3, io.SeekCurrent); err != nil {
				return 0, 0, err
			}
			continue
		}

		bitrate := mp3BitrateTable[bitrateIdx]
		sampleRate := mp3SampleRateTable[sampleRateIdx]
		samplesPerFrame := 1152

		frameSize := (samplesPerFrame/8*bitrate*1000)/sampleRate + int(padding)

		// Check for Xing/VBRI header inside this frame.
		// Xing header is at offset 36 from frame start for MPEG1 stereo, 21 for mono.
		// We're 4 bytes in, so seek to potential Xing offsets.
		frameStart, _ := f.Seek(-4, io.SeekCurrent)

		// Try Xing at side info offsets: 32 bytes for stereo, 17 for mono (after 4-byte header).
		channelMode := (syncBuf[3] >> 6) & 0x03
		xingOffset := int64(32 + 4) // stereo
		if channelMode == 3 {       // mono
			xingOffset = 17 + 4
		}

		if _, err := f.Seek(frameStart+xingOffset, io.SeekStart); err != nil {
			return 0, 0, err
		}

		var xingTag [4]byte
		if _, err := io.ReadFull(f, xingTag[:]); err != nil {
			return 0, 0, err
		}

		if string(xingTag[:]) == "Xing" || string(xingTag[:]) == "Info" {
			var flags [4]byte
			io.ReadFull(f, flags[:])
			flagVal := binary.BigEndian.Uint32(flags[:])

			if flagVal&0x01 != 0 { // frames field present
				var framesBuf [4]byte
				io.ReadFull(f, framesBuf[:])
				totalFrames := int(binary.BigEndian.Uint32(framesBuf[:]))
				totalSamples := totalFrames * samplesPerFrame
				dur := totalSamples / sampleRate

				// Calculate average bitrate.
				stat, _ := f.Stat()
				fileSize := stat.Size()
				avgBitrate := 0
				if dur > 0 {
					avgBitrate = int(fileSize*8/1000) / dur
				}
				return dur, avgBitrate, nil
			}
		}

		// No VBR header — CBR estimation from file size.
		stat, _ := f.Stat()
		fileSize := stat.Size()
		audioBytesEst := fileSize - frameStart
		dur := int(audioBytesEst * 8 / int64(bitrate) / 1000)
		_ = frameSize
		return dur, bitrate, nil
	}

	return 0, 0, io.ErrUnexpectedEOF
}

// parseFLACDuration reads the STREAMINFO metadata block to compute duration.
func parseFLACDuration(path string) (durationSecs, bitrateKbps int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	// Verify fLaC marker.
	var marker [4]byte
	if _, err := io.ReadFull(f, marker[:]); err != nil {
		return 0, 0, err
	}
	if string(marker[:]) != "fLaC" {
		return 0, 0, io.ErrUnexpectedEOF
	}

	// Read first metadata block header (should be STREAMINFO).
	var blockHeader [4]byte
	if _, err := io.ReadFull(f, blockHeader[:]); err != nil {
		return 0, 0, err
	}

	blockType := blockHeader[0] & 0x7F
	if blockType != 0 { // 0 = STREAMINFO
		return 0, 0, io.ErrUnexpectedEOF
	}

	// STREAMINFO is 34 bytes.
	var si [34]byte
	if _, err := io.ReadFull(f, si[:]); err != nil {
		return 0, 0, err
	}

	// Bytes 10-17: sample rate (20 bits), channels (3 bits), bps (5 bits), total samples (36 bits).
	sampleRate := int(si[10])<<12 | int(si[11])<<4 | int(si[12])>>4
	totalSamples := int64(si[13]&0x0F)<<32 | int64(si[14])<<24 | int64(si[15])<<16 | int64(si[16])<<8 | int64(si[17])

	if sampleRate == 0 {
		return 0, 0, io.ErrUnexpectedEOF
	}

	dur := int(totalSamples / int64(sampleRate))

	// Bitrate from file size.
	stat, _ := f.Stat()
	bitrate := 0
	if dur > 0 {
		bitrate = int(stat.Size()*8/1000) / dur
	}
	return dur, bitrate, nil
}

// ffprobe fallback — log once if ffprobe not found.
var (
	ffprobeChecked  bool
	ffprobeAvail    bool
	ffprobeCheckMu  sync.Mutex
)

type ffprobeOutput struct {
	Format struct {
		Duration string `json:"duration"`
		BitRate  string `json:"bit_rate"`
	} `json:"format"`
}

func ffprobeDuration(path string, log *slog.Logger) (durationSecs, bitrateKbps int, err error) {
	ffprobeCheckMu.Lock()
	if !ffprobeChecked {
		ffprobeChecked = true
		_, err := exec.LookPath("ffprobe")
		ffprobeAvail = err == nil
		if !ffprobeAvail {
			log.Warn("ffprobe not found in PATH, duration estimation may be inaccurate for non-MP3/FLAC files")
		}
	}
	avail := ffprobeAvail
	ffprobeCheckMu.Unlock()

	if !avail {
		return 0, 0, exec.ErrNotFound
	}

	out, err := exec.Command("ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		path,
	).Output()
	if err != nil {
		return 0, 0, err
	}

	var result ffprobeOutput
	if err := json.Unmarshal(out, &result); err != nil {
		return 0, 0, err
	}

	// Parse duration string (float seconds).
	var durFloat float64
	for i, c := range result.Format.Duration {
		if c == '.' || (c >= '0' && c <= '9') {
			continue
		}
		result.Format.Duration = result.Format.Duration[:i]
		break
	}
	_, _ = io.Discard, durFloat // suppress unused
	durFloat = 0
	for i := 0; i < len(result.Format.Duration); i++ {
		if result.Format.Duration[i] == '.' {
			frac := result.Format.Duration[i+1:]
			fracVal := 0.0
			div := 10.0
			for _, c := range frac {
				fracVal += float64(c-'0') / div
				div *= 10
			}
			durFloat += fracVal
			break
		}
		durFloat = durFloat*10 + float64(result.Format.Duration[i]-'0')
	}

	dur := int(math.Round(durFloat))

	// Parse bitrate.
	br := 0
	for _, c := range result.Format.BitRate {
		if c >= '0' && c <= '9' {
			br = br*10 + int(c-'0')
		}
	}
	br /= 1000

	return dur, br, nil
}
