package library_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	musiclib "listen-party/internal/library"

	_ "modernc.org/sqlite"
)

func TestArtworkReadsEmbeddedPicture(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "with-art.mp3")
	want := []byte{0xff, 0xd8, 0xff, 0xd9}
	if err := os.WriteFile(path, id3v23PictureTag(want), 0o644); err != nil {
		t.Fatalf("write mp3: %v", err)
	}

	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{dir}, 1)
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	defer lib.Close()
	if err := lib.Scan(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}
	tracks, err := lib.Search(ctx, "with art")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	got, mimeType, err := lib.Artwork(ctx, tracks[0].ID)
	if err != nil {
		t.Fatalf("artwork: %v", err)
	}
	if mimeType != "image/jpeg" || string(got) != string(want) {
		t.Fatalf("artwork = %q %v, want image/jpeg %v", mimeType, got, want)
	}
}

func TestScanIndexesFilenameFallbackAndSearch(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "Alex Clare - Too Close [zP5OEwh31E4].mp3")
	if err := os.WriteFile(path, []byte("not really mp3"), 0o644); err != nil {
		t.Fatalf("write mp3: %v", err)
	}

	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{dir}, 1)
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	defer lib.Close()
	if err := lib.Scan(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}

	tracks, err := lib.Search(ctx, "too-close")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("search returned %d tracks, want 1", len(tracks))
	}
	if tracks[0].Title != "Too Close" || tracks[0].Artist != "Alex Clare" {
		t.Fatalf("track = %q/%q, want Too Close/Alex Clare", tracks[0].Title, tracks[0].Artist)
	}
}

func TestSearchOrdersByTitleAscending(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	for _, name := range []string{"Mix - Zeta.mp3", "Mix - alpha.mp3", "Mix - Middle.mp3"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("not really mp3"), 0o644); err != nil {
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
	tracks, err := lib.Search(ctx, "mix")
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

func TestRandomTrackExcludesLogicalKeys(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	for _, name := range []string{"Artist - First.mp3", "Artist - Second.mp3"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{dir}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	if err := lib.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	tracks, err := lib.Search(ctx, "Artist")
	if err != nil || len(tracks) != 2 {
		t.Fatalf("tracks = %#v, err = %v", tracks, err)
	}
	got, err := lib.RandomTrack(ctx, []string{tracks[0].DedupeKey})
	if err != nil {
		t.Fatal(err)
	}
	if got.DedupeKey != tracks[1].DedupeKey {
		t.Fatalf("random key = %q, want %q", got.DedupeKey, tracks[1].DedupeKey)
	}
}

func TestSearchFieldFiltersTitleArtistAndAlbum(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	files := map[string][]byte{
		"Alex Clare - Too Close.mp3": []byte("not really mp3"),
		"album.mp3":                  id3v23TextTag(map[string]string{"TIT2": "Blue Line", "TPE1": "Massive Attack", "TALB": "Protection"}),
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

	titleMatches, err := lib.SearchField(ctx, "alex", "title")
	if err != nil {
		t.Fatalf("search title: %v", err)
	}
	if len(titleMatches) != 0 {
		t.Fatalf("title search returned %d tracks, want 0", len(titleMatches))
	}
	artistMatches, err := lib.SearchField(ctx, "alex", "artist")
	if err != nil {
		t.Fatalf("search artist: %v", err)
	}
	if len(artistMatches) != 1 || artistMatches[0].Artist != "Alex Clare" {
		t.Fatalf("artist search = %#v, want Alex Clare match", artistMatches)
	}
	albumMatches, err := lib.SearchField(ctx, "protection", "album")
	if err != nil {
		t.Fatalf("search album: %v", err)
	}
	if len(albumMatches) != 1 || albumMatches[0].Album != "Protection" {
		t.Fatalf("album search = %#v, want Protection match", albumMatches)
	}
	allFieldMatches, err := lib.SearchField(ctx, "massive protection", "")
	if err != nil {
		t.Fatalf("search all fields: %v", err)
	}
	if len(allFieldMatches) != 1 || allFieldMatches[0].Title != "Blue Line" {
		t.Fatalf("all-field search = %#v, want Blue Line match", allFieldMatches)
	}
}

func TestSearchDeduplicatesCopiedTracks(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	rootA := filepath.Join(root, "a")
	rootB := filepath.Join(root, "b")
	for _, dir := range []string{rootA, rootB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	mtime := fixedModTime()
	for _, path := range []string{
		filepath.Join(rootA, "Artist - Same Song.mp3"),
		filepath.Join(rootB, "Artist - Same Song.mp3"),
	} {
		if err := os.WriteFile(path, []byte("same bytes"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("chtimes %s: %v", path, err)
		}
	}

	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{rootA, rootB}, 1)
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
	if count != 2 {
		t.Fatalf("physical track count = %d, want 2", count)
	}
	tracks, err := lib.Search(ctx, "same")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("deduped search returned %d tracks, want 1", len(tracks))
	}
}

func TestPlaylistResolvesRemainingDuplicateAfterRescan(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	rootA := filepath.Join(root, "a")
	rootB := filepath.Join(root, "b")
	for _, dir := range []string{rootA, rootB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	mtime := fixedModTime()
	pathA := filepath.Join(rootA, "Artist - Same Song.mp3")
	pathB := filepath.Join(rootB, "Artist - Same Song.mp3")
	for _, path := range []string{pathA, pathB} {
		if err := os.WriteFile(path, []byte("same bytes"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("chtimes %s: %v", path, err)
		}
	}

	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{rootA, rootB}, 1)
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	defer lib.Close()
	if err := lib.Scan(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}
	tracks, err := lib.Search(ctx, "same")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	playlist, err := lib.CreatePlaylist(ctx, "Favorites", "user1")
	if err != nil {
		t.Fatalf("create playlist: %v", err)
	}
	if _, err := lib.AddPlaylistTrack(ctx, playlist.ID, tracks[0].DedupeKey); err != nil {
		t.Fatalf("add playlist track: %v", err)
	}
	if err := os.Remove(pathA); err != nil {
		t.Fatalf("remove duplicate: %v", err)
	}
	if err := lib.ScanDir(ctx, rootA); err != nil {
		t.Fatalf("scan dir: %v", err)
	}
	resolved, err := lib.ResolvePlaylistTracks(ctx, playlist.ID)
	if err != nil {
		t.Fatalf("resolve playlist: %v", err)
	}
	if len(resolved) != 1 || resolved[0].Title != "Same Song" {
		t.Fatalf("resolved tracks = %#v, want remaining duplicate", resolved)
	}
	items, err := lib.PlaylistItems(ctx, playlist.ID)
	if err != nil {
		t.Fatalf("playlist items: %v", err)
	}
	if len(items) != 1 || items[0].DedupeKey != resolved[0].DedupeKey {
		t.Fatalf("playlist item dedupe key = %#v, want %q", items, resolved[0].DedupeKey)
	}
}

func TestRemovePlaylistItemAndPlaylist(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "Artist - Track.mp3")
	if err := os.WriteFile(path, []byte("not really mp3"), 0o644); err != nil {
		t.Fatalf("write mp3: %v", err)
	}
	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{dir}, 1)
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	defer lib.Close()
	if err := lib.Scan(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}
	tracks, err := lib.Search(ctx, "track")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	playlist, err := lib.CreatePlaylist(ctx, "Favorites", "user1")
	if err != nil {
		t.Fatalf("create playlist: %v", err)
	}
	item, err := lib.AddPlaylistTrack(ctx, playlist.ID, tracks[0].DedupeKey)
	if err != nil {
		t.Fatalf("add playlist track: %v", err)
	}
	items, err := lib.PlaylistItems(ctx, playlist.ID)
	if err != nil {
		t.Fatalf("playlist items: %v", err)
	}
	if len(items) != 1 || items[0].DedupeKey != tracks[0].DedupeKey {
		t.Fatalf("playlist item dedupe key = %#v, want %q", items, tracks[0].DedupeKey)
	}
	if err := lib.RemovePlaylistItem(ctx, playlist.ID, item.ID); err != nil {
		t.Fatalf("remove playlist item: %v", err)
	}
	items, err = lib.PlaylistItems(ctx, playlist.ID)
	if err != nil {
		t.Fatalf("playlist items: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("playlist items after remove = %#v, want empty", items)
	}
	if _, err := lib.AddPlaylistTrack(ctx, playlist.ID, tracks[0].DedupeKey); err != nil {
		t.Fatalf("re-add playlist track: %v", err)
	}
	if err := lib.DeletePlaylist(ctx, playlist.ID); err != nil {
		t.Fatalf("delete playlist: %v", err)
	}
	if _, err := lib.GetPlaylist(ctx, playlist.ID); !errors.Is(err, musiclib.ErrPlaylistNotFound) {
		t.Fatalf("get deleted playlist error = %v, want ErrPlaylistNotFound", err)
	}
	items, err = lib.PlaylistItems(ctx, playlist.ID)
	if err != nil || len(items) != 0 {
		t.Fatalf("deleted playlist items = %#v, %v; want none", items, err)
	}
	if err := lib.DeletePlaylist(ctx, playlist.ID); !errors.Is(err, musiclib.ErrPlaylistNotFound) {
		t.Fatalf("delete missing playlist error = %v, want ErrPlaylistNotFound", err)
	}
}

func TestImportPlaylistFolderMatchesIndexedManifest(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dir := filepath.Join(root, "Legacy Friday")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	names := []string{"01 - First.mp3", "02 - Second.mp3"}
	manifest := make([]musiclib.FolderManifestFile, 0, len(names)+1)
	for _, name := range names {
		data := []byte("contents-" + name)
		fullPath := filepath.Join(dir, name)
		if err := os.WriteFile(fullPath, data, 0o644); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(fullPath)
		if err != nil {
			t.Fatal(err)
		}
		manifest = append(manifest, musiclib.FolderManifestFile{
			RelativePath: filepath.ToSlash(filepath.Join("Legacy Friday", name)),
			Size:         len64(data), LastModifiedMS: info.ModTime().UnixMilli(),
		})
	}
	manifest = append(manifest, musiclib.FolderManifestFile{RelativePath: "Legacy Friday/Missing.mp3", Size: 99})
	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{root}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	if err := lib.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	playlist, err := lib.CreatePlaylist(ctx, "Friday", "owner")
	if err != nil {
		t.Fatal(err)
	}
	result, err := lib.ImportPlaylistFolder(ctx, playlist.ID, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 2 || result.Unmatched != 1 || result.Ambiguous != 0 {
		t.Fatalf("first import = %#v", result)
	}
	result, err = lib.ImportPlaylistFolder(ctx, playlist.ID, manifest[:2])
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 0 || result.Duplicates != 2 {
		t.Fatalf("second import = %#v", result)
	}
	playlist, err = lib.GetPlaylist(ctx, playlist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(playlist.Items) != 2 || playlist.Items[0].Title != "First" || playlist.Items[1].Title != "Second" {
		t.Fatalf("playlist items = %#v", playlist.Items)
	}
}

func len64(value []byte) int64 { return int64(len(value)) }

func TestSQLiteFTS5Available(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "tracks.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE VIRTUAL TABLE fts_check USING fts5(value)`); err != nil {
		t.Fatalf("create fts5 table: %v", err)
	}
}

func fixedModTime() time.Time {
	return time.Unix(1_700_000_000, 0)
}

func TestScanSkipsUnchangedFiles(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "same.mp3")
	if err := os.WriteFile(path, []byte("not really mp3"), 0o644); err != nil {
		t.Fatalf("write mp3: %v", err)
	}

	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{dir}, 1)
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	defer lib.Close()
	if err := lib.Scan(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if err := lib.Scan(ctx); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if status := lib.ScanStatus(); status.Unchanged != 1 {
		t.Fatalf("unchanged = %d, want 1", status.Unchanged)
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

	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{dir}, 1)
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
}

func TestScanDirDeletesOnlyMissingTracksUnderThatDirectory(t *testing.T) {
	ctx := context.Background()
	rootA := t.TempDir()
	rootB := t.TempDir()
	removePath := filepath.Join(rootA, "remove.mp3")
	keepPath := filepath.Join(rootB, "keep.mp3")
	for _, path := range []string{removePath, keepPath} {
		if err := os.WriteFile(path, []byte("not really mp3"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{rootA, rootB}, 1)
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
	if err := lib.ScanDir(ctx, rootA); err != nil {
		t.Fatalf("scan dir: %v", err)
	}
	count, err := lib.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("count after scoped delete = %d, want 1", count)
	}
	tracks, err := lib.Search(ctx, "keep")
	if err != nil {
		t.Fatalf("search keep: %v", err)
	}
	if len(tracks) != 1 || tracks[0].Title != "keep" {
		t.Fatalf("remaining tracks = %#v, want keep", tracks)
	}
}

func id3v23PictureTag(image []byte) []byte {
	body := append([]byte{0, 'i', 'm', 'a', 'g', 'e', '/', 'j', 'p', 'e', 'g', 0, 3, 0}, image...)
	frame := []byte{'A', 'P', 'I', 'C', byte(len(body) >> 24), byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body)), 0, 0}
	frame = append(frame, body...)
	size := len(frame)
	return append([]byte{'I', 'D', '3', 3, 0, 0, byte(size >> 21), byte(size >> 14), byte(size >> 7), byte(size)}, frame...)
}

func id3v23TextTag(frames map[string]string) []byte {
	var body []byte
	for id, text := range frames {
		payload := append([]byte{0}, []byte(text)...)
		frame := []byte{id[0], id[1], id[2], id[3], byte(len(payload) >> 24), byte(len(payload) >> 16), byte(len(payload) >> 8), byte(len(payload)), 0, 0}
		body = append(body, append(frame, payload...)...)
	}
	size := len(body)
	return append([]byte{'I', 'D', '3', 3, 0, 0, byte(size >> 21), byte(size >> 14), byte(size >> 7), byte(size)}, body...)
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

	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{dir}, 1)
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
