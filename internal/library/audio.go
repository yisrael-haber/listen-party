package library

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dhowden/tag"
)

const trackMetaVersion = 2

type audioFormat struct {
	name        string
	contentType string
}

var audioFormats = map[string]audioFormat{
	".mp3":  {name: "mp3", contentType: "audio/mpeg"},
	".flac": {name: "flac", contentType: "audio/flac"},
	".wav":  {name: "wav", contentType: "audio/wav"},
}

func SupportedExtensions() []string {
	extensions := make([]string, 0, len(audioFormats))
	for ext := range audioFormats {
		extensions = append(extensions, ext)
	}
	sort.Strings(extensions)
	return extensions
}

func audioExtension(path string) string {
	return strings.ToLower(filepath.Ext(path))
}

func isSupportedAudio(path string) bool {
	_, ok := audioFormats[audioExtension(path)]
	return ok
}

func formatName(path string) string {
	if format, ok := audioFormats[audioExtension(path)]; ok {
		return format.name
	}
	return ""
}

func contentType(path string) string {
	if format, ok := audioFormats[audioExtension(path)]; ok {
		return format.contentType
	}
	return "application/octet-stream"
}

type fileMetadata struct {
	Title       string
	Artist      string
	Album       string
	AlbumArtist string
	TrackNo     int
	DiscNo      int
}

func readMetadata(f *os.File, path string) (fileMetadata, error) {
	if audioExtension(path) == ".wav" {
		info, err := readWAVInfo(f)
		if err != nil {
			return fileMetadata{}, err
		}
		return info.metadata, nil
	}
	meta, err := tag.ReadFrom(f)
	if err != nil {
		return fileMetadata{}, err
	}
	trackNo, _ := meta.Track()
	discNo, _ := meta.Disc()
	return fileMetadata{
		Title:       meta.Title(),
		Artist:      meta.Artist(),
		Album:       meta.Album(),
		AlbumArtist: meta.AlbumArtist(),
		TrackNo:     trackNo,
		DiscNo:      discNo,
	}, nil
}

func trackDurationMS(path string) int64 {
	switch audioExtension(path) {
	case ".flac":
		return flacDurationMS(path)
	case ".wav":
		return wavDurationMS(path)
	default:
		return mp3DurationMS(path)
	}
}

func durationFromSamples(totalSamples uint64, sampleRate uint32) int64 {
	if totalSamples == 0 || sampleRate == 0 {
		return 0
	}
	return int64(totalSamples * 1000 / uint64(sampleRate))
}

func readFull(r io.Reader, n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
