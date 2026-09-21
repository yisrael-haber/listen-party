package library_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	musiclib "listen-party/backend/internal/library"

	_ "modernc.org/sqlite"
)

func validTestMP3(prefix []byte) []byte {
	frame := make([]byte, 417)
	copy(frame, []byte{0xff, 0xfb, 0x90, 0x64})
	return append(append([]byte(nil), prefix...), bytes.Repeat(frame, 12)...)
}

func TestArtworkReadsEmbeddedPicture(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "with-art.mp3")
	want := []byte{0xff, 0xd8, 0xff, 0xd9}
	if err := os.WriteFile(path, validTestMP3(id3v23PictureTag(want)), 0o644); err != nil {
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
	if err := os.WriteFile(path, validTestMP3(nil), 0o644); err != nil {
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

func TestScanSkipsMalformedSupportedAudio(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "good.mp3"), validTestMP3(nil), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.ogg"), []byte("not audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{dir}, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	if err := lib.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	count, err := lib.Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("count = %d, %v; want 1", count, err)
	}
	if status := lib.ScanStatus(); status.Skipped != 1 || status.Parsed != 1 {
		t.Fatalf("status = %#v; want one parsed and one skipped", status)
	}
}

func TestSchemaResetClearsContentStateAndPreservesPlaylists(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Artist - Track.mp3"), validTestMP3(nil), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "tracks.sqlite")
	lib, err := musiclib.Open(ctx, dbPath, []string{dir}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	tracks, err := lib.Search(ctx, "track")
	if err != nil || len(tracks) != 1 {
		t.Fatalf("tracks = %#v, %v", tracks, err)
	}
	playlist, err := lib.CreatePlaylist(ctx, "Keep me", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddPlaylistTrack(ctx, playlist.ID, tracks[0].ContentKey); err != nil {
		t.Fatal(err)
	}
	if err := lib.SaveRoomPlaybackSnapshot(ctx, musiclib.RoomPlaybackSnapshot{RoomID: "main", Revision: 1, State: []byte(`{"current":1}`)}); err != nil {
		t.Fatal(err)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Force schema creation to fail after the drops, then verify rollback.
	if _, err := db.Exec(`
DELETE FROM library_metadata WHERE key = 'schema_version';
DROP INDEX tracks_content_idx;
CREATE TABLE tracks_content_idx (value TEXT);`); err != nil {
		t.Fatal(err)
	}
	if failed, err := musiclib.Open(ctx, dbPath, []string{dir}, 1); err == nil {
		failed.Close()
		t.Fatal("expected schema creation failure")
	}
	for _, table := range []string{"tracks", "tracks_fts", "playlist_items", "room_playback_state"} {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s after rollback: count=%d, err=%v", table, count, err)
		}
	}
	var versionCount int
	if err := db.QueryRow("SELECT count(*) FROM library_metadata").Scan(&versionCount); err != nil || versionCount != 0 {
		t.Fatalf("schema version committed on failure: count=%d, err=%v", versionCount, err)
	}
	if _, err := db.Exec("DROP TABLE tracks_content_idx"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	lib, err = musiclib.Open(ctx, dbPath, []string{dir}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	if count, err := lib.Count(ctx); err != nil || count != 0 {
		t.Fatalf("count after reset = %d, %v; want 0", count, err)
	}
	if got, err := lib.GetPlaylist(ctx, playlist.ID); err != nil || len(got.Items) != 0 || got.Name != playlist.Name {
		t.Fatalf("playlist after reset = %#v, %v", got, err)
	}
	if snapshots, err := lib.LoadRoomPlaybackSnapshots(ctx); err != nil || len(snapshots) != 0 {
		t.Fatalf("snapshots after reset = %#v, %v", snapshots, err)
	}
	// A subsequent startup must preserve the rebuilt index.
	if err := lib.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	lib, err = musiclib.Open(ctx, dbPath, []string{dir}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	if count, err := lib.Count(ctx); err != nil || count != 1 {
		t.Fatalf("count after restart = %d, %v; want 1", count, err)
	}
}

func TestLegacyMigrationPreservesTracksAndPlaylists(t *testing.T) {
	for _, version := range []string{"2", "3"} {
		t.Run("v"+version, func(t *testing.T) {
			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "tracks.sqlite")
			db, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`
CREATE TABLE library_metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT INTO library_metadata VALUES ('schema_version', '2');
CREATE TABLE tracks (
	id INTEGER PRIMARY KEY, path TEXT NOT NULL UNIQUE, title TEXT NOT NULL, artist TEXT NOT NULL, album TEXT NOT NULL,
	track_no INTEGER NOT NULL DEFAULT 0, duration_ms INTEGER NOT NULL DEFAULT 0, size INTEGER NOT NULL, mod_time INTEGER NOT NULL,
	dedupe_key TEXT NOT NULL DEFAULT '', match_key TEXT NOT NULL DEFAULT '', available INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE playlists (id INTEGER PRIMARY KEY, name TEXT NOT NULL, owner_id TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE playlist_items (
	id INTEGER PRIMARY KEY, playlist_id INTEGER NOT NULL, position INTEGER NOT NULL, dedupe_key TEXT NOT NULL, match_key TEXT NOT NULL,
	title TEXT NOT NULL, artist TEXT NOT NULL, album TEXT NOT NULL
);
CREATE TABLE room_playback_state (room_id TEXT PRIMARY KEY, revision INTEGER NOT NULL, state_json BLOB NOT NULL, updated_at INTEGER NOT NULL);
INSERT INTO tracks VALUES (1, '/music/artist-track.mp3', 'Track', 'Artist', 'Album', 1, 0, 42, 1, 'artist|track|42|1', 'artist|track', 1);
INSERT INTO playlists VALUES (1, 'Saved', 'owner', 1, 1);
INSERT INTO playlist_items VALUES (1, 1, 1, 'artist|track|42|1', 'artist|track', 'Track', 'Artist', 'Album');
INSERT INTO playlist_items VALUES (2, 1, 2, 'missing', 'missing', 'Missing', 'Artist', 'Album');
INSERT INTO room_playback_state VALUES ('main', 1, '{}', 1);`); err != nil {
				db.Close()
				t.Fatal(err)
			}
			if version == "3" {
				if _, err := db.Exec(`
ALTER TABLE tracks ADD COLUMN content_key TEXT NOT NULL DEFAULT '';
ALTER TABLE tracks ADD COLUMN lossless INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tracks ADD COLUMN bitrate_bps INTEGER NOT NULL DEFAULT 0;
UPDATE tracks SET content_key = 'legacy:' || id;
UPDATE library_metadata SET value = '3' WHERE key = 'schema_version';`); err != nil {
					db.Close()
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			lib, err := musiclib.Open(ctx, dbPath, nil, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer lib.Close()
			playlist, err := lib.GetPlaylist(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			if playlist.Name != "Saved" || len(playlist.Items) != 1 || playlist.Items[0].ID != 1 || playlist.Items[0].Position != 1 || playlist.Items[0].ContentKey == "" {
				t.Fatalf("migrated playlist items = %#v", playlist.Items)
			}
			track, err := lib.ResolveContentKey(ctx, playlist.Items[0].ContentKey)
			if err != nil || track.ID != 1 {
				t.Fatalf("resolve migrated content key: %#v, %v", track, err)
			}
			snapshots, err := lib.LoadRoomPlaybackSnapshots(ctx)
			if err != nil || len(snapshots) != 0 {
				t.Fatalf("migrated snapshots = %#v, %v", snapshots, err)
			}
			// Restart must preserve newly written items and playback snapshots.
			if _, err := lib.AddPlaylistTrack(ctx, 1, track.ContentKey); err != nil {
				t.Fatal(err)
			}
			if err := lib.SaveRoomPlaybackSnapshot(ctx, musiclib.RoomPlaybackSnapshot{RoomID: "main", Revision: 2, State: []byte("{}")}); err != nil {
				t.Fatal(err)
			}
			if err := lib.Close(); err != nil {
				t.Fatal(err)
			}
			lib, err = musiclib.Open(ctx, dbPath, nil, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer lib.Close()
			if items, err := lib.PlaylistItems(ctx, 1); err != nil || len(items) != 2 {
				t.Fatalf("items after restart = %#v, %v", items, err)
			}
			if snapshots, err := lib.LoadRoomPlaybackSnapshots(ctx); err != nil || len(snapshots) != 1 {
				t.Fatalf("snapshots after restart = %#v, %v", snapshots, err)
			}
		})
	}
}

func TestSearchOrdersByTitleAscending(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	for _, name := range []string{"Mix - Zeta.mp3", "Mix - alpha.mp3", "Mix - Middle.mp3"} {
		if err := os.WriteFile(filepath.Join(dir, name), validTestMP3(nil), 0o644); err != nil {
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

func TestShuffleContentKeysListEachLogicalTrack(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	for _, name := range []string{"Artist - First.mp3", "Artist - Second.mp3"} {
		if err := os.WriteFile(filepath.Join(dir, name), validTestMP3(nil), 0o644); err != nil {
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
	keys, err := lib.ShuffleContentKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != len(tracks) {
		t.Fatalf("shuffle keys = %v, want one per logical track", keys)
	}
	for _, key := range keys {
		if _, err := lib.ResolveContentKey(ctx, key); err != nil {
			t.Fatalf("resolve shuffle key %q: %v", key, err)
		}
	}
}

func TestPlaylistShuffleContentKeysResolveAvailableItems(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Artist - Selected.mp3"), validTestMP3(nil), 0o644); err != nil {
		t.Fatal(err)
	}
	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{dir}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	if err := lib.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	tracks, err := lib.Search(ctx, "Selected")
	if err != nil || len(tracks) != 1 {
		t.Fatalf("tracks = %#v, err = %v", tracks, err)
	}
	playlist, err := lib.CreatePlaylist(ctx, "Selected", "owner")
	if err != nil {
		t.Fatal(err)
	}
	item, err := lib.AddPlaylistTrack(ctx, playlist.ID, tracks[0].ContentKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddPlaylistTrack(ctx, playlist.ID, tracks[0].ContentKey); err != nil {
		t.Fatal(err)
	}
	keys, err := lib.PlaylistShuffleContentKeys(ctx, playlist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != item.ContentKey {
		t.Fatalf("playlist shuffle keys = %v, want [%q]", keys, item.ContentKey)
	}
	got, err := lib.ResolveContentKey(ctx, keys[0])
	if err != nil {
		t.Fatal(err)
	}
	if got.ContentKey != tracks[0].ContentKey {
		t.Fatalf("playlist item key = %q, want %q", got.ContentKey, tracks[0].ContentKey)
	}
	if err := lib.RemovePlaylistItem(ctx, playlist.ID, item.ID); err != nil {
		t.Fatal(err)
	}
	keys, err = lib.PlaylistShuffleContentKeys(ctx, playlist.ID)
	if err != nil || len(keys) != 1 {
		t.Fatalf("playlist shuffle keys after removal = %v, %v", keys, err)
	}
}

func TestPlaylistShuffleContentKeysAllowEmptyPlaylist(t *testing.T) {
	ctx := context.Background()
	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{t.TempDir()}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	playlist, err := lib.CreatePlaylist(ctx, "Empty", "owner")
	if err != nil {
		t.Fatal(err)
	}
	ids, err := lib.PlaylistShuffleContentKeys(ctx, playlist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("empty playlist shuffle IDs = %v, want none", ids)
	}
}

func TestSearchFieldFiltersTitleArtistAndAlbum(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	files := map[string][]byte{
		"Alex Clare - Too Close.mp3": validTestMP3(nil),
		"album.mp3":                  validTestMP3(id3v23TextTag(map[string]string{"TIT2": "Blue Line", "TPE1": "Massive Attack", "TALB": "Protection"})),
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
		if err := os.WriteFile(path, validTestMP3(nil), 0o644); err != nil {
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
	ids, err := lib.ShuffleContentKeys(ctx)
	if err != nil {
		t.Fatalf("shuffle IDs: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("shuffle IDs = %v, want one logical track", ids)
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
		if err := os.WriteFile(path, validTestMP3(nil), 0o644); err != nil {
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
	if _, err := lib.AddPlaylistTrack(ctx, playlist.ID, tracks[0].ContentKey); err != nil {
		t.Fatalf("add playlist track: %v", err)
	}
	if err := os.Remove(pathA); err != nil {
		t.Fatalf("remove duplicate: %v", err)
	}
	if err := lib.ScanDir(ctx, rootA); err != nil {
		t.Fatalf("scan dir: %v", err)
	}
	items, err := lib.PlaylistItems(ctx, playlist.ID)
	if err != nil {
		t.Fatalf("playlist items: %v", err)
	}
	if len(items) != 1 || items[0].ContentKey != tracks[0].ContentKey {
		t.Fatalf("playlist items = %#v, want original content key", items)
	}
	resolved, err := lib.ResolveContentKey(ctx, items[0].ContentKey)
	if err != nil || resolved.Title != "Same Song" || resolved.ID == tracks[0].ID {
		t.Fatalf("resolved track = %#v, %v; want remaining duplicate", resolved, err)
	}
}

func TestRemovePlaylistItemAndPlaylist(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "Artist - Track.mp3")
	if err := os.WriteFile(path, validTestMP3(nil), 0o644); err != nil {
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
	item, err := lib.AddPlaylistTrack(ctx, playlist.ID, tracks[0].ContentKey)
	if err != nil {
		t.Fatalf("add playlist track: %v", err)
	}
	items, err := lib.PlaylistItems(ctx, playlist.ID)
	if err != nil {
		t.Fatalf("playlist items: %v", err)
	}
	if len(items) != 1 || items[0].ContentKey != tracks[0].ContentKey {
		t.Fatalf("playlist item dedupe key = %#v, want %q", items, tracks[0].ContentKey)
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
	if _, err := lib.AddPlaylistTrack(ctx, playlist.ID, tracks[0].ContentKey); err != nil {
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
		data := validTestMP3(nil)
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

func TestImportPlaylistKeysReportsAndSkipsInvalidEntries(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for _, name := range []string{"Artist - First.mp3", "Artist - Second.mp3"} {
		if err := os.WriteFile(filepath.Join(root, name), validTestMP3(nil), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{root}, 1)
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
	playlist, err := lib.CreatePlaylist(ctx, "Imported", "owner")
	if err != nil {
		t.Fatal(err)
	}
	result, err := lib.ImportPlaylistKeys(ctx, playlist.ID, []string{
		tracks[1].ContentKey, "", "missing-key", tracks[0].ContentKey, tracks[1].ContentKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 2 || result.Invalid != 1 || result.Unavailable != 1 || result.Duplicates != 1 {
		t.Fatalf("import result = %#v", result)
	}
	playlist, err = lib.GetPlaylist(ctx, playlist.ID)
	if err != nil || len(playlist.Items) != 2 {
		t.Fatalf("playlist = %#v, err = %v", playlist, err)
	}
	if playlist.Items[0].ContentKey != tracks[1].ContentKey || playlist.Items[1].ContentKey != tracks[0].ContentKey {
		t.Fatalf("playlist order = %#v", playlist.Items)
	}
	result, err = lib.ImportPlaylistKeys(ctx, playlist.ID, []string{tracks[0].ContentKey})
	if err != nil || result.Imported != 0 || result.Duplicates != 1 {
		t.Fatalf("existing-key import = %#v, %v", result, err)
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
	if err := os.WriteFile(path, validTestMP3(nil), 0o644); err != nil {
		t.Fatalf("write mp3: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "tracks.sqlite")
	lib, err := musiclib.Open(ctx, dbPath, []string{dir}, 1)
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

	// An unchanged file still needs parsing when its stored properties are old.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE tracks SET properties_version = 0, bitrate_bps = 0"); err != nil {
		t.Fatal(err)
	}
	if err := lib.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if status := lib.ScanStatus(); status.Parsed != 1 {
		t.Fatalf("property refresh status = %#v", status)
	}
	tracks, err := lib.Search(ctx, "same")
	if err != nil || len(tracks) != 1 || tracks[0].Bitrate == 0 {
		t.Fatalf("refreshed tracks = %#v, %v", tracks, err)
	}
}

func TestScanDeletesMissingTracksAfterSuccessfulWalk(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	keepPath := filepath.Join(dir, "keep.mp3")
	removePath := filepath.Join(dir, "remove.mp3")
	for _, path := range []string{keepPath, removePath} {
		if err := os.WriteFile(path, validTestMP3(nil), 0o644); err != nil {
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
		if err := os.WriteFile(path, validTestMP3(nil), 0o644); err != nil {
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
		if err := os.WriteFile(path, validTestMP3(nil), 0o644); err != nil {
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
