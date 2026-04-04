package api

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
)

// MP3 frame header tables — duplicated from scanner/duration.go (unexported there).
var (
	sliceBitrateV1     = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
	sliceBitrateV2     = [16]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
	sliceSampleRateV1  = [4]int{44100, 48000, 32000, 0}
	sliceSampleRateV2  = [4]int{22050, 24000, 16000, 0}
	sliceSampleRateV25 = [4]int{11025, 12000, 8000, 0}
)

const sliceMaxSync = 65536

var sliceChunkPool = sync.Pool{
	New: func() any { return make([]byte, sliceMaxSync+256) },
}

// mp3FrameInfo holds parsed information about the first MP3 frame and optional VBR header.
type mp3FrameInfo struct {
	audioStart      int64
	bitrate         int // kbps (first-frame or average)
	sampleRate      int
	samplesPerFrame int
	totalFrames     int     // from Xing header; 0 if CBR
	totalDuration   float64 // seconds
	toc             [100]byte
	hasTOC          bool
	audioBytes      int64 // fileSize - audioStart
}

// parseMP3Info extracts frame info needed for slicing.
func parseMP3Info(f *os.File, fileSize int64) (mp3FrameInfo, error) {
	var info mp3FrameInfo

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return info, err
	}
	var header [10]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return info, err
	}
	var audioOffset int64
	if header[0] == 'I' && header[1] == 'D' && header[2] == '3' {
		audioOffset = int64(header[6])<<21 | int64(header[7])<<14 | int64(header[8])<<7 | int64(header[9]) + 10
	}

	if _, err := f.Seek(audioOffset, io.SeekStart); err != nil {
		return info, err
	}
	chunkSize := min(int64(sliceMaxSync+256), fileSize-audioOffset)
	if chunkSize <= 4 {
		return info, io.ErrUnexpectedEOF
	}
	chunk := sliceChunkPool.Get().([]byte)
	defer sliceChunkPool.Put(chunk)
	n, err := io.ReadAtLeast(f, chunk[:chunkSize], 4)
	if err != nil {
		return info, err
	}
	chunk = chunk[:n]

	for i := 0; i < len(chunk)-4; i++ {
		if chunk[i] != 0xFF || chunk[i+1]&0xE0 != 0xE0 {
			continue
		}

		version := (chunk[i+1] >> 3) & 0x03
		layer := (chunk[i+1] >> 1) & 0x03
		if version == 1 || layer != 1 { // reserved version or non-Layer III
			continue
		}

		bitrateIdx := (chunk[i+2] >> 4) & 0x0F
		sampleRateIdx := (chunk[i+2] >> 2) & 0x03
		if bitrateIdx == 0 || bitrateIdx == 15 || sampleRateIdx == 3 {
			continue
		}

		var xingStereoOff, xingMonoOff int
		if version == 3 { // MPEG1
			info.bitrate = sliceBitrateV1[bitrateIdx]
			info.sampleRate = sliceSampleRateV1[sampleRateIdx]
			info.samplesPerFrame = 1152
			xingStereoOff = 32 + 4
			xingMonoOff = 17 + 4
		} else { // MPEG2 or MPEG2.5
			info.bitrate = sliceBitrateV2[bitrateIdx]
			if version == 2 {
				info.sampleRate = sliceSampleRateV2[sampleRateIdx]
			} else {
				info.sampleRate = sliceSampleRateV25[sampleRateIdx]
			}
			info.samplesPerFrame = 576
			xingStereoOff = 17 + 4
			xingMonoOff = 9 + 4
		}

		info.audioStart = audioOffset + int64(i)
		info.audioBytes = fileSize - info.audioStart

		channelMode := (chunk[i+3] >> 6) & 0x03
		xingOff := xingStereoOff
		if channelMode == 3 { // mono
			xingOff = xingMonoOff
		}

		if xingIdx := i + xingOff; xingIdx+12 <= len(chunk) {
			tag := string(chunk[xingIdx : xingIdx+4])
			if tag == "Xing" || tag == "Info" {
				flags := binary.BigEndian.Uint32(chunk[xingIdx+4 : xingIdx+8])
				if flags&0x01 != 0 { // frames field present
					info.totalFrames = int(binary.BigEndian.Uint32(chunk[xingIdx+8 : xingIdx+12]))
					if info.sampleRate > 0 {
						info.totalDuration = float64(info.totalFrames) * float64(info.samplesPerFrame) / float64(info.sampleRate)
					}
				}
				if flags&0x04 != 0 { // TOC present
					tocOff := xingIdx + 8
					if flags&0x01 != 0 {
						tocOff += 4 // skip frames field
					}
					if flags&0x02 != 0 {
						tocOff += 4 // skip bytes field
					}
					if tocOff+100 <= len(chunk) {
						copy(info.toc[:], chunk[tocOff:tocOff+100])
						info.hasTOC = true
					}
				}
			}
		}

		// CBR duration estimate if no Xing frame count.
		if info.totalDuration == 0 && info.bitrate > 0 {
			info.totalDuration = float64(info.audioBytes) * 8 / float64(info.bitrate) / 1000
		}

		return info, nil
	}

	return info, fmt.Errorf("no valid MP3 frame sync found")
}

// tocLookup interpolates the Xing TOC to find byte offset for a given time fraction (0..1).
func tocLookup(toc [100]byte, audioBytes int64, fraction float64) int64 {
	if fraction <= 0 {
		return 0
	}
	if fraction >= 1 {
		return audioBytes
	}
	idx := fraction * 100
	i := int(idx)
	if i >= 99 {
		return int64(float64(toc[99]) / 256.0 * float64(audioBytes))
	}
	frac := idx - float64(i)
	a := float64(toc[i])
	b := float64(toc[i+1])
	pos := (a + frac*(b-a)) / 256.0
	return int64(pos * float64(audioBytes))
}

// sliceMP3 writes MP3 frames covering [startTime, startTime+duration] to w.
// Returns true if handled, false to fall back to full-file serve.
func sliceMP3(w http.ResponseWriter, f *os.File, fileSize int64, contentType string, startTime, duration float64) bool {
	info, err := parseMP3Info(f, fileSize)
	if err != nil || info.sampleRate == 0 {
		return false
	}

	endTime := startTime + duration
	if info.totalDuration > 0 && endTime > info.totalDuration {
		endTime = info.totalDuration
	}
	if startTime >= endTime {
		return false
	}

	var startByte, endByte int64

	if info.hasTOC && info.totalDuration > 0 {
		// VBR with TOC — interpolate byte positions.
		startByte = info.audioStart + tocLookup(info.toc, info.audioBytes, startTime/info.totalDuration)
		endByte = info.audioStart + tocLookup(info.toc, info.audioBytes, endTime/info.totalDuration)
	} else if info.bitrate > 0 {
		// CBR — direct byte offset calculation.
		bytesPerSec := float64(info.bitrate) * 1000 / 8
		startByte = info.audioStart + int64(startTime*bytesPerSec)
		endByte = info.audioStart + int64(endTime*bytesPerSec)
	} else {
		return false
	}

	if startByte < info.audioStart {
		startByte = info.audioStart
	}
	if endByte > fileSize {
		endByte = fileSize
	}
	if startByte >= endByte {
		return false
	}

	if _, err := f.Seek(startByte, io.SeekStart); err != nil {
		return false
	}

	length := endByte - startByte
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	io.Copy(w, io.LimitReader(f, length))
	return true
}

// flacSeekPoint matches the on-disk SEEKTABLE entry layout.
type flacSeekPoint struct {
	sampleNumber uint64
	byteOffset   uint64 // relative to first audio frame
	numSamples   uint16
}

// seekPointFloor returns the byte offset of the last seek point at or before targetSample.
func seekPointFloor(points []flacSeekPoint, targetSample uint64) uint64 {
	var best uint64
	for _, p := range points {
		if p.sampleNumber == 0xFFFFFFFFFFFFFFFF { // placeholder
			continue
		}
		if p.sampleNumber <= targetSample {
			best = p.byteOffset
		} else {
			break // sorted
		}
	}
	return best
}

// seekPointCeil returns the byte offset of the first seek point after targetSample,
// or audioSize if none exists.
func seekPointCeil(points []flacSeekPoint, targetSample uint64, audioSize int64) uint64 {
	for _, p := range points {
		if p.sampleNumber == 0xFFFFFFFFFFFFFFFF {
			continue
		}
		if p.sampleNumber > targetSample {
			return p.byteOffset
		}
	}
	return uint64(audioSize)
}

// sliceFLAC uses the SEEKTABLE to extract a time range from a FLAC file.
// Returns true if handled, false to fall back to full-file serve.
func sliceFLAC(w http.ResponseWriter, f *os.File, fileSize int64, contentType string, startTime, duration float64) bool {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false
	}

	var marker [4]byte
	if _, err := io.ReadFull(f, marker[:]); err != nil || string(marker[:]) != "fLaC" {
		return false
	}

	var streamInfo [34]byte
	var seekPoints []flacSeekPoint
	var audioDataStart int64
	foundStreamInfo := false

	pos := int64(4) // after "fLaC"
	for {
		var bh [4]byte
		if _, err := io.ReadFull(f, bh[:]); err != nil {
			return false
		}
		isLast := bh[0]&0x80 != 0
		blockType := bh[0] & 0x7F
		blockLen := int64(bh[1])<<16 | int64(bh[2])<<8 | int64(bh[3])
		pos += 4

		switch blockType {
		case 0: // STREAMINFO
			if blockLen != 34 {
				return false
			}
			if _, err := io.ReadFull(f, streamInfo[:]); err != nil {
				return false
			}
			foundStreamInfo = true
		case 3: // SEEKTABLE
			numPoints := blockLen / 18
			seekPoints = make([]flacSeekPoint, numPoints)
			for i := range seekPoints {
				var sp [18]byte
				if _, err := io.ReadFull(f, sp[:]); err != nil {
					return false
				}
				seekPoints[i] = flacSeekPoint{
					sampleNumber: binary.BigEndian.Uint64(sp[0:8]),
					byteOffset:   binary.BigEndian.Uint64(sp[8:16]),
					numSamples:   binary.BigEndian.Uint16(sp[16:18]),
				}
			}
		default:
			if _, err := f.Seek(blockLen, io.SeekCurrent); err != nil {
				return false
			}
		}
		pos += blockLen
		if isLast {
			audioDataStart = pos
			break
		}
	}

	if !foundStreamInfo || len(seekPoints) == 0 {
		return false
	}

	sampleRate := int(streamInfo[10])<<12 | int(streamInfo[11])<<4 | int(streamInfo[12])>>4
	if sampleRate == 0 {
		return false
	}
	totalSamples := int64(streamInfo[13]&0x0F)<<32 | int64(streamInfo[14])<<24 |
		int64(streamInfo[15])<<16 | int64(streamInfo[16])<<8 | int64(streamInfo[17])
	totalDuration := float64(totalSamples) / float64(sampleRate)

	endTime := startTime + duration
	if endTime > totalDuration {
		endTime = totalDuration
	}
	if startTime >= endTime {
		return false
	}

	audioSize := fileSize - audioDataStart
	startSample := uint64(startTime * float64(sampleRate))
	endSample := uint64(endTime * float64(sampleRate))

	startByteOff := seekPointFloor(seekPoints, startSample)
	endByteOff := seekPointCeil(seekPoints, endSample, audioSize)

	absStart := audioDataStart + int64(startByteOff)
	absEnd := audioDataStart + int64(endByteOff)
	if absEnd > fileSize {
		absEnd = fileSize
	}
	if absStart >= absEnd {
		return false
	}

	w.Header().Set("Content-Type", contentType)
	// fLaC marker + STREAMINFO block (4-byte header + 34 bytes) + audio
	audioLen := absEnd - absStart
	totalLen := 4 + 4 + 34 + audioLen
	w.Header().Set("Content-Length", strconv.FormatInt(totalLen, 10))

	// Emit minimal FLAC: marker + STREAMINFO (marked as last metadata block) + audio frames.
	w.Write([]byte("fLaC"))
	w.Write([]byte{0x80, 0, 0, 34}) // STREAMINFO, last block, 34 bytes
	w.Write(streamInfo[:])

	if _, err := f.Seek(absStart, io.SeekStart); err != nil {
		return false
	}
	io.Copy(w, io.LimitReader(f, audioLen))
	return true
}

// sliceWAV extracts a time range from a WAV file by rewriting RIFF headers.
// Returns true if handled, false to fall back to full-file serve.
func sliceWAV(w http.ResponseWriter, f *os.File, fileSize int64, contentType string, startTime, duration float64) bool {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false
	}

	var riff [12]byte
	if _, err := io.ReadFull(f, riff[:]); err != nil {
		return false
	}
	if string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return false
	}

	var fmtData []byte
	var dataOffset, dataSize int64
	var sampleRate, blockAlign int

	pos := int64(12)
	for pos < fileSize-8 {
		if _, err := f.Seek(pos, io.SeekStart); err != nil {
			break
		}
		var ch [8]byte
		if _, err := io.ReadFull(f, ch[:]); err != nil {
			break
		}
		chunkID := string(ch[0:4])
		chunkSize := int64(binary.LittleEndian.Uint32(ch[4:8]))

		switch chunkID {
		case "fmt ":
			if chunkSize < 16 || chunkSize > 1024 {
				return false
			}
			fmtData = make([]byte, chunkSize)
			if _, err := io.ReadFull(f, fmtData); err != nil {
				return false
			}
			sampleRate = int(binary.LittleEndian.Uint32(fmtData[4:8]))
			blockAlign = int(binary.LittleEndian.Uint16(fmtData[12:14]))
		case "data":
			dataOffset = pos + 8
			dataSize = chunkSize
		}

		if fmtData != nil && dataOffset > 0 {
			break
		}
		pos += 8 + chunkSize
		if chunkSize%2 != 0 {
			pos++ // WAV chunks pad to even byte boundaries
		}
	}

	if sampleRate == 0 || blockAlign == 0 || dataSize == 0 {
		return false
	}

	totalDuration := float64(dataSize) / float64(sampleRate*blockAlign)
	endTime := startTime + duration
	if endTime > totalDuration {
		endTime = totalDuration
	}
	if startTime >= endTime {
		return false
	}

	// Block-aligned byte offsets within the data chunk.
	startByte := int64(startTime*float64(sampleRate)) * int64(blockAlign)
	endByte := int64(endTime*float64(sampleRate)) * int64(blockAlign)
	if endByte > dataSize {
		endByte = dataSize
	}
	sliceSize := endByte - startByte
	if sliceSize <= 0 {
		return false
	}

	// RIFF header(12) + fmt chunk(8 + fmtLen) + data chunk(8 + sliceSize)
	fmtLen := int64(len(fmtData))
	totalLen := 12 + 8 + fmtLen + 8 + sliceSize
	riffSize := uint32(totalLen - 8) // excludes "RIFF" + size field itself

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(totalLen, 10))

	// RIFF header
	var riffHeader [12]byte
	copy(riffHeader[0:4], "RIFF")
	binary.LittleEndian.PutUint32(riffHeader[4:8], riffSize)
	copy(riffHeader[8:12], "WAVE")
	w.Write(riffHeader[:])

	// fmt chunk
	var fmtHeader [8]byte
	copy(fmtHeader[0:4], "fmt ")
	binary.LittleEndian.PutUint32(fmtHeader[4:8], uint32(fmtLen))
	w.Write(fmtHeader[:])
	w.Write(fmtData)

	// data chunk header + sliced PCM
	var dataHeader [8]byte
	copy(dataHeader[0:4], "data")
	binary.LittleEndian.PutUint32(dataHeader[4:8], uint32(sliceSize))
	w.Write(dataHeader[:])

	if _, err := f.Seek(dataOffset+startByte, io.SeekStart); err != nil {
		return false
	}
	io.Copy(w, io.LimitReader(f, sliceSize))
	return true
}
