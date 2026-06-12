package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeSearch(t *testing.T) {
	got := normalizeSearch(" The_Band - Track  01! ")
	want := "the band track 01"
	if got != want {
		t.Fatalf("normalizeSearch() = %q, want %q", got, want)
	}
}

func TestIsMP3(t *testing.T) {
	for _, path := range []string{"song.mp3", "SONG.MP3", "/tmp/a.b/song.Mp3"} {
		if !isMP3(path) {
			t.Fatalf("%q should be accepted", path)
		}
	}
	for _, path := range []string{"song.flac", "mp3.txt", "song"} {
		if isMP3(path) {
			t.Fatalf("%q should be rejected", path)
		}
	}
}

func TestShouldIgnoreDir(t *testing.T) {
	for _, name := range []string{".git", ".cache", "__pycache__", "__MACOSX", "node_modules", "System Volume Information", "$RECYCLE.BIN"} {
		if !shouldIgnoreDir(name) {
			t.Fatalf("%q should be ignored", name)
		}
	}
	for _, name := range []string{"music", "Albums", "01 - live", "_single_underscore"} {
		if shouldIgnoreDir(name) {
			t.Fatalf("%q should not be ignored", name)
		}
	}
}

func TestClampTrackQueryLimit(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{name: "default", limit: 0, want: defaultTrackQueryLimit},
		{name: "negative", limit: -1, want: defaultTrackQueryLimit},
		{name: "inside limit", limit: 80, want: 80},
		{name: "max", limit: 100, want: 100},
		{name: "too high", limit: 101, want: maxTrackQueryLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampTrackQueryLimit(tt.limit); got != tt.want {
				t.Fatalf("clampTrackQueryLimit(%d) = %d, want %d", tt.limit, got, tt.want)
			}
		})
	}
}

func TestSearchOrdersByTitleAscending(t *testing.T) {
	ctx := context.Background()
	lib, err := Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), nil, 1)
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	defer lib.Close()
	tracksToSeed := []Track{
		{path: "/music/z.mp3", Title: "Zeta", Artist: "A", Album: "music", Size: 1, ModTime: time.Unix(1, 0)},
		{path: "/music/a.mp3", Title: "alpha", Artist: "Z", Album: "music", Size: 1, ModTime: time.Unix(2, 0)},
		{path: "/music/m.mp3", Title: "Middle", Artist: "M", Album: "music", Size: 1, ModTime: time.Unix(3, 0)},
	}
	if err := lib.flushTracks(ctx, tracksToSeed); err != nil {
		t.Fatalf("flush tracks: %v", err)
	}

	tracks, err := lib.Search(ctx, "music")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(tracks) != 3 {
		t.Fatalf("search returned %d tracks, want 3", len(tracks))
	}
	got := []string{tracks[0].Title, tracks[1].Title, tracks[2].Title}
	want := []string{"alpha", "Middle", "Zeta"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("search order = %#v, want %#v", got, want)
		}
	}
}

func TestFlushTracksWritesPartialBatch(t *testing.T) {
	ctx := context.Background()
	lib, err := Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), nil, 1)
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	defer lib.Close()

	tracks := []Track{
		{path: "/music/one.mp3", Title: "One", Size: 1, ModTime: time.Unix(1, 0)},
		{path: "/music/two.mp3", Title: "Two", Size: 1, ModTime: time.Unix(2, 0)},
	}
	if err := lib.flushTracks(ctx, tracks); err != nil {
		t.Fatalf("flush tracks: %v", err)
	}
	count, err := lib.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != int64(len(tracks)) {
		t.Fatalf("count = %d, want %d", count, len(tracks))
	}
}

func TestWriteScannedTracksFlushesFinalPartialBatch(t *testing.T) {
	ctx := context.Background()
	lib, err := Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), nil, 1)
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	defer lib.Close()

	tracks := make(chan Track, 2)
	var indexed int64
	tracks <- Track{path: "/music/one.mp3", Title: "One", Size: 1, ModTime: time.Unix(1, 0)}
	tracks <- Track{path: "/music/two.mp3", Title: "Two", Size: 1, ModTime: time.Unix(2, 0)}
	close(tracks)
	if err := lib.writeScannedTracks(ctx, tracks, &indexed); err != nil {
		t.Fatalf("write scanned tracks: %v", err)
	}
	if indexed != 2 {
		t.Fatalf("indexed = %d, want 2", indexed)
	}
	count, err := lib.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}

func TestScanReturnsAlreadyInProgress(t *testing.T) {
	ctx := context.Background()
	lib, err := Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), nil, 1)
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	defer lib.Close()

	lib.scanMu.Lock()
	defer lib.scanMu.Unlock()
	if err := lib.Scan(ctx); !errors.Is(err, ErrScanInProgress) {
		t.Fatalf("Scan() error = %v, want %v", err, ErrScanInProgress)
	}
}

func TestScanSkipsUnchangedFiles(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "same.mp3")
	if err := os.WriteFile(path, []byte("not really mp3"), 0o644); err != nil {
		t.Fatalf("write mp3: %v", err)
	}

	lib, err := Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{dir}, 1)
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	defer lib.Close()
	if err := lib.Scan(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if _, err := lib.db.ExecContext(ctx, `UPDATE tracks SET title = 'Kept Title', search_text = 'kept title' WHERE path = ?`, path); err != nil {
		t.Fatalf("update indexed title: %v", err)
	}

	if err := lib.Scan(ctx); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	tracks, err := lib.Search(ctx, "kept")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(tracks) != 1 || tracks[0].Title != "Kept Title" {
		t.Fatalf("unchanged scan reparsed track, got %#v", tracks)
	}
}

func TestScanDeletesMissingTracksAfterSuccessfulWalk(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	keepPath := filepath.Join(dir, "keep.mp3")
	removePath := filepath.Join(dir, "remove.mp3")
	for _, path := range []string{keepPath, removePath} {
		if err := os.WriteFile(path, []byte("not really mp3"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	lib, err := Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{dir}, 1)
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	defer lib.Close()
	if err := lib.Scan(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if err := os.Remove(removePath); err != nil {
		t.Fatalf("remove mp3: %v", err)
	}
	if err := lib.Scan(ctx); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	count, err := lib.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("count after delete = %d, want 1", count)
	}
	var rows int
	if err := lib.db.QueryRowContext(ctx, `SELECT count(*) FROM tracks`).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("stored rows after delete = %d, want 1", rows)
	}
}

func TestScanSkipsIgnoredDirectories(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	visiblePath := filepath.Join(dir, "visible.mp3")
	hiddenDir := filepath.Join(dir, ".git")
	hiddenPath := filepath.Join(hiddenDir, "hidden.mp3")
	nodeDir := filepath.Join(dir, "node_modules")
	nodePath := filepath.Join(nodeDir, "dependency.mp3")
	for _, path := range []string{hiddenDir, nodeDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}
	for _, path := range []string{visiblePath, hiddenPath, nodePath} {
		if err := os.WriteFile(path, []byte("not really mp3"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	lib, err := Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{dir}, 1)
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	defer lib.Close()
	if err := lib.Scan(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}
	count, err := lib.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}
