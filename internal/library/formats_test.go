package library_test

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	musiclib "listen-party/internal/library"
)

func clearAlbumMetadata(ctx context.Context, databasePath string) error {
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, `UPDATE tracks SET album = '', album_artist = '', album_key = '', meta_version = 0`)
	return err
}

func TestScanIndexesFLACAndWAVWithDurations(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Artist - Lossless.flac"), flacFile(44100, 44100), 0o644); err != nil {
		t.Fatalf("write flac: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "waveform.wav"), wavFile(8000, 8000, map[string]string{
		"INAM": "Wave Song",
		"IART": "Wave Artist",
		"IPRD": "Wave Album",
	}), 0o644); err != nil {
		t.Fatalf("write wav: %v", err)
	}

	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{dir}, 1)
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	defer lib.Close()
	if err := lib.Scan(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}

	flacTracks, err := lib.Search(ctx, "lossless")
	if err != nil || len(flacTracks) != 1 {
		t.Fatalf("flac search = %#v, err = %v", flacTracks, err)
	}
	if flacTracks[0].Format != "flac" {
		t.Fatalf("flac format = %q, want flac", flacTracks[0].Format)
	}
	flacTrack, err := lib.Get(ctx, flacTracks[0].ID)
	if err != nil {
		t.Fatalf("get flac: %v", err)
	}
	if flacTrack.DurationMS != 1000 {
		t.Fatalf("flac duration = %d, want 1000", flacTrack.DurationMS)
	}

	wavTracks, err := lib.SearchField(ctx, "wave song", "title")
	if err != nil || len(wavTracks) != 1 {
		t.Fatalf("wav search = %#v, err = %v", wavTracks, err)
	}
	wavTrack, err := lib.Get(ctx, wavTracks[0].ID)
	if err != nil {
		t.Fatalf("get wav: %v", err)
	}
	if wavTrack.Format != "wav" || wavTrack.Artist != "Wave Artist" || wavTrack.Album != "Wave Album" {
		t.Fatalf("wav track = %#v, want RIFF INFO metadata", wavTrack)
	}
	if wavTrack.DurationMS != 1000 {
		t.Fatalf("wav duration = %d, want 1000", wavTrack.DurationMS)
	}
}

func TestAlbumsGroupByAlbumArtist(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	files := map[string][]byte{
		"first.mp3": id3v23TextTag(map[string]string{
			"TIT2": "Better Things", "TPE1": "Tracey Thorn", "TPE2": "Massive Attack",
			"TALB": "Protection", "TRCK": "2",
		}),
		"second.mp3": id3v23TextTag(map[string]string{
			"TIT2": "Karmacoma", "TPE1": "Massive Attack", "TPE2": "Massive Attack",
			"TALB": "Protection", "TRCK": "1",
		}),
		"other.mp3": id3v23TextTag(map[string]string{
			"TIT2": "Teardrop", "TPE1": "Massive Attack", "TALB": "Mezzanine",
		}),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{dir}, 1)
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	defer lib.Close()
	if err := lib.Scan(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}

	albums, err := lib.ListAlbums(ctx, "")
	if err != nil {
		t.Fatalf("list albums: %v", err)
	}
	if len(albums) != 2 {
		t.Fatalf("albums = %#v, want Mezzanine and Protection", albums)
	}
	if albums[0].Name != "Mezzanine" || albums[1].Name != "Protection" {
		t.Fatalf("album order = %q, %q", albums[0].Name, albums[1].Name)
	}
	protection := albums[1]
	if protection.TrackCount != 2 || protection.Artist != "Massive Attack" {
		t.Fatalf("protection = %#v, want 2 tracks by the album artist", protection)
	}

	filtered, err := lib.ListAlbums(ctx, "mezz")
	if err != nil || len(filtered) != 1 || filtered[0].Name != "Mezzanine" {
		t.Fatalf("filtered albums = %#v, err = %v", filtered, err)
	}

	album, tracks, err := lib.Album(ctx, protection.Key)
	if err != nil {
		t.Fatalf("album: %v", err)
	}
	if album.TrackCount != 2 || len(tracks) != 2 {
		t.Fatalf("album tracks = %#v", tracks)
	}
	if tracks[0].Title != "Karmacoma" || tracks[1].Title != "Better Things" {
		t.Fatalf("album track order = %q, %q; want track-number order", tracks[0].Title, tracks[1].Title)
	}

	if _, _, err := lib.Album(ctx, "missing|album"); err == nil {
		t.Fatal("missing album error = nil, want ErrAlbumNotFound")
	}
}

func TestScanRereadsTracksIndexedByAnOlderMetadataVersion(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "tagged.mp3")
	if err := os.WriteFile(path, id3v23TextTag(map[string]string{
		"TIT2": "Angel", "TPE1": "Massive Attack", "TALB": "Mezzanine",
	}), 0o644); err != nil {
		t.Fatalf("write mp3: %v", err)
	}
	databasePath := filepath.Join(t.TempDir(), "tracks.sqlite")
	lib, err := musiclib.Open(ctx, databasePath, []string{dir}, 1)
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	if err := lib.Scan(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if err := lib.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := clearAlbumMetadata(ctx, databasePath); err != nil {
		t.Fatalf("clear album metadata: %v", err)
	}
	lib, err = musiclib.Open(ctx, databasePath, []string{dir}, 1)
	if err != nil {
		t.Fatalf("reopen library: %v", err)
	}
	defer lib.Close()
	if err := lib.Scan(ctx); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if status := lib.ScanStatus(); status.Unchanged != 0 {
		t.Fatalf("unchanged = %d, want the stale row to be re-read", status.Unchanged)
	}
	albums, err := lib.ListAlbums(ctx, "")
	if err != nil || len(albums) != 1 || albums[0].Name != "Mezzanine" {
		t.Fatalf("albums after rescan = %#v, err = %v", albums, err)
	}
}

func TestCancelScanReleasesTheLibraryForARestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	const files = 120
	for i := range files {
		name := fmt.Sprintf("Artist - Track %03d.mp3", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{dir}, 1)
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	defer lib.Close()

	if lib.CancelScan() {
		t.Fatal("CancelScan reported a running scan before any scan started")
	}

	done := make(chan error, 1)
	go func() { done <- lib.Scan(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for !lib.ScanStatus().Scanning && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	lib.CancelScan()
	scanErr := <-done
	if scanErr != nil && !errors.Is(scanErr, context.Canceled) {
		t.Fatalf("scan error = %v, want nil or context.Canceled", scanErr)
	}
	status := lib.ScanStatus()
	if status.Scanning {
		t.Fatal("scan still reported as running after it returned")
	}
	if errors.Is(scanErr, context.Canceled) {
		if status.Stage != musiclib.ScanStageCanceled {
			t.Fatalf("stage = %q, want %q", status.Stage, musiclib.ScanStageCanceled)
		}
		if status.LastError != "" {
			t.Fatalf("last_error = %q, want a cancellation to not read as a failure", status.LastError)
		}
	}

	if err := lib.RestartScan(ctx); err != nil {
		t.Fatalf("restart scan: %v", err)
	}
	if status := lib.ScanStatus(); status.Stage != musiclib.ScanStageComplete {
		t.Fatalf("stage after restart = %q, want %q", status.Stage, musiclib.ScanStageComplete)
	}
	count, err := lib.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != files {
		t.Fatalf("indexed %d tracks after restart, want %d", count, files)
	}
}

func flacFile(sampleRate uint32, totalSamples uint64) []byte {
	block := make([]byte, 34)
	block[10] = byte(sampleRate >> 12)
	block[11] = byte(sampleRate >> 4)
	block[12] = byte(sampleRate<<4) | 0x02 // sample rate remainder, 2 channels
	block[13] = byte(totalSamples >> 32 & 0x0f)
	binary.BigEndian.PutUint32(block[14:18], uint32(totalSamples))

	file := []byte("fLaC")
	file = append(file, 0x80, 0, 0, byte(len(block))) // last block, STREAMINFO
	return append(file, block...)
}

func wavFile(byteRate uint32, dataSize uint32, info map[string]string) []byte {
	fmtChunk := make([]byte, 16)
	binary.LittleEndian.PutUint16(fmtChunk[0:2], 1)
	binary.LittleEndian.PutUint16(fmtChunk[2:4], 1)
	binary.LittleEndian.PutUint32(fmtChunk[4:8], byteRate)
	binary.LittleEndian.PutUint32(fmtChunk[8:12], byteRate)
	binary.LittleEndian.PutUint16(fmtChunk[12:14], 1)
	binary.LittleEndian.PutUint16(fmtChunk[14:16], 8)

	body := append(riffChunk("fmt ", fmtChunk), riffChunk("LIST", riffInfo(info))...)
	body = append(body, riffChunk("data", make([]byte, dataSize))...)

	header := make([]byte, 12)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(4+len(body)))
	copy(header[8:12], "WAVE")
	return append(header, body...)
}

func riffInfo(info map[string]string) []byte {
	body := []byte("INFO")
	for _, id := range []string{"INAM", "IART", "IPRD"} {
		value, ok := info[id]
		if !ok {
			continue
		}
		body = append(body, riffChunk(id, append([]byte(value), 0))...)
	}
	return body
}

func riffChunk(id string, payload []byte) []byte {
	chunk := make([]byte, 8, 8+len(payload)+1)
	copy(chunk[0:4], id)
	binary.LittleEndian.PutUint32(chunk[4:8], uint32(len(payload)))
	chunk = append(chunk, payload...)
	if len(payload)%2 == 1 {
		chunk = append(chunk, 0)
	}
	return chunk
}
