package library

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"os"
)

const flacStreamInfoSize = 34

var errNotFLAC = errors.New("not a flac stream")

func flacDurationMS(path string) int64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	samples, sampleRate, err := flacStreamInfo(bufio.NewReader(f))
	if err != nil {
		return 0
	}
	return durationFromSamples(samples, sampleRate)
}

func flacStreamInfo(r io.Reader) (totalSamples uint64, sampleRate uint32, err error) {
	marker, err := readFull(r, 4)
	if err != nil {
		return 0, 0, err
	}
	if string(marker[:3]) == "ID3" {
		rest, err := readFull(r, 6)
		if err != nil {
			return 0, 0, err
		}
		size := syncSafeSize(rest[2:])
		if _, err := io.CopyN(io.Discard, r, int64(size)); err != nil {
			return 0, 0, err
		}
		if marker, err = readFull(r, 4); err != nil {
			return 0, 0, err
		}
	}
	if string(marker) != "fLaC" {
		return 0, 0, errNotFLAC
	}

	for {
		header, err := readFull(r, 4)
		if err != nil {
			return 0, 0, err
		}
		last := header[0]&0x80 != 0
		blockType := header[0] & 0x7f
		length := int64(header[1])<<16 | int64(header[2])<<8 | int64(header[3])
		if blockType == 0 {
			if length < flacStreamInfoSize {
				return 0, 0, errNotFLAC
			}
			block, err := readFull(r, flacStreamInfoSize)
			if err != nil {
				return 0, 0, err
			}
			sampleRate = uint32(block[10])<<12 | uint32(block[11])<<4 | uint32(block[12])>>4
			totalSamples = uint64(block[13]&0x0f)<<32 | uint64(binary.BigEndian.Uint32(block[14:18]))
			return totalSamples, sampleRate, nil
		}
		if last {
			return 0, 0, errNotFLAC
		}
		if _, err := io.CopyN(io.Discard, r, length); err != nil {
			return 0, 0, err
		}
	}
}

func syncSafeSize(b []byte) uint32 {
	var size uint32
	for _, value := range b {
		size = size<<7 | uint32(value&0x7f)
	}
	return size
}
