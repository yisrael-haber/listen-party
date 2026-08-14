package library

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/dhowden/tag"
)

const (
	maxWAVChunkRead = 8 << 20
	wavHeaderSize   = 12
	wavFmtMinSize   = 16
)

var errNotWAV = errors.New("not a wav stream")

type wavInfo struct {
	metadata   fileMetadata
	durationMS int64
}

func wavDurationMS(path string) int64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	info, err := readWAVInfo(f)
	if err != nil {
		return 0
	}
	return info.durationMS
}

func readWAVInfo(rs io.ReadSeeker) (wavInfo, error) {
	if _, err := rs.Seek(0, io.SeekStart); err != nil {
		return wavInfo{}, err
	}
	header, err := readFull(rs, wavHeaderSize)
	if err != nil {
		return wavInfo{}, err
	}
	riff := string(header[0:4])
	if (riff != "RIFF" && riff != "RF64") || string(header[8:12]) != "WAVE" {
		return wavInfo{}, errNotWAV
	}

	var info wavInfo
	var byteRate, dataSize int64
	for {
		chunk, err := readFull(rs, 8)
		if err != nil {
			break
		}
		id := string(chunk[0:4])
		size := int64(binary.LittleEndian.Uint32(chunk[4:8]))
		read := int64(0)
		switch {
		case id == "fmt " && size >= wavFmtMinSize:
			body, err := readFull(rs, wavFmtMinSize)
			if err != nil {
				return wavInfo{}, err
			}
			read = wavFmtMinSize
			byteRate = int64(binary.LittleEndian.Uint32(body[8:12]))
			if byteRate == 0 {
				sampleRate := int64(binary.LittleEndian.Uint32(body[4:8]))
				channels := int64(binary.LittleEndian.Uint16(body[2:4]))
				bits := int64(binary.LittleEndian.Uint16(body[14:16]))
				byteRate = sampleRate * channels * bits / 8
			}
		case id == "data":
			dataSize = size
		case id == "LIST" && size <= maxWAVChunkRead:
			body, err := readFull(rs, int(size))
			if err != nil {
				return wavInfo{}, err
			}
			read = size
			mergeWAVListInfo(&info.metadata, body)
		case (id == "id3 " || id == "ID3 ") && size <= maxWAVChunkRead:
			body, err := readFull(rs, int(size))
			if err != nil {
				return wavInfo{}, err
			}
			read = size
			mergeWAVID3(&info.metadata, body)
		}
		skip := size - read
		if size%2 == 1 {
			skip++
		}
		if skip > 0 {
			if _, err := rs.Seek(skip, io.SeekCurrent); err != nil {
				break
			}
		}
	}
	if byteRate > 0 && dataSize > 0 {
		info.durationMS = dataSize * 1000 / byteRate
	}
	return info, nil
}

func mergeWAVListInfo(meta *fileMetadata, body []byte) {
	if len(body) < 4 || string(body[0:4]) != "INFO" {
		return
	}
	offset := 4
	for offset+8 <= len(body) {
		id := string(body[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(body[offset+4 : offset+8]))
		offset += 8
		if size < 0 || offset+size > len(body) {
			return
		}
		value := wavText(body[offset : offset+size])
		offset += size
		if size%2 == 1 {
			offset++
		}
		if value == "" {
			continue
		}
		switch id {
		case "INAM":
			meta.Title = value
		case "IART":
			meta.Artist = value
		case "IPRD", "IALB":
			meta.Album = value
		case "ITRK", "IPRT":
			if number, err := strconv.Atoi(strings.SplitN(value, "/", 2)[0]); err == nil {
				meta.TrackNo = number
			}
		}
	}
}

func mergeWAVID3(meta *fileMetadata, body []byte) {
	parsed, err := tag.ReadFrom(bytes.NewReader(body))
	if err != nil {
		return
	}
	if value := parsed.Title(); value != "" {
		meta.Title = value
	}
	if value := parsed.Artist(); value != "" {
		meta.Artist = value
	}
	if value := parsed.Album(); value != "" {
		meta.Album = value
	}
	if value := parsed.AlbumArtist(); value != "" {
		meta.AlbumArtist = value
	}
	if number, _ := parsed.Track(); number > 0 {
		meta.TrackNo = number
	}
	if number, _ := parsed.Disc(); number > 0 {
		meta.DiscNo = number
	}
}

func wavText(value []byte) string {
	return strings.TrimSpace(string(bytes.TrimRight(value, "\x00")))
}
