package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	musiclib "listen-party/internal/library"
)

func albumTestServer(t *testing.T, permissions ...RoomPermission) (*Server, []musiclib.Album) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	tags := []map[string]string{
		{"TIT2": "Karmacoma", "TPE1": "Massive Attack", "TPE2": "Massive Attack", "TALB": "Protection", "TRCK": "1"},
		{"TIT2": "Better Things", "TPE1": "Tracey Thorn", "TPE2": "Massive Attack", "TALB": "Protection", "TRCK": "2"},
		{"TIT2": "Loose Ends", "TPE1": "Someone Else"},
	}
	for i, frames := range tags {
		name := fmt.Sprintf("track-%d.mp3", i)
		if err := os.WriteFile(filepath.Join(dir, name), id3v23Frames(frames), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{dir}, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lib.Close() })
	if err := lib.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	albums, err := lib.ListAlbums(ctx, "")
	if err != nil || len(albums) != 1 {
		t.Fatalf("albums = %#v, err = %v", albums, err)
	}
	server := testServer(&Server{
		Auth:    fakeAuth{user: UserInfo{Username: "alice", Groups: []string{"staff"}}},
		Library: lib,
		Config: Config{Rooms: []Room{{
			ID: "main", Name: "Main", Grants: map[string][]RoomPermission{"staff": permissions},
		}}},
	})
	t.Cleanup(server.Rooms.Close)
	return server, albums
}

func TestAlbumEndpointsListAndResolveTracks(t *testing.T) {
	server, albums := albumTestServer(t, PermissionQueueAdd)
	handler := server.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/albums?q=protect", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("albums status = %d: %s", rec.Code, rec.Body.String())
	}
	var listed []musiclib.Album
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].TrackCount != 2 || listed[0].Artist != "Massive Attack" {
		t.Fatalf("listed albums = %#v", listed)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/albums/"+url.PathEscape(albums[0].Key), nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("album status = %d: %s", rec.Code, rec.Body.String())
	}
	var detail struct {
		musiclib.Album
		Tracks []musiclib.Track `json:"tracks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Tracks) != 2 || detail.Tracks[0].Title != "Karmacoma" {
		t.Fatalf("album detail = %#v", detail)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/albums/missing", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing album status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestQueueAlbumUsesQueueAddPermission(t *testing.T) {
	server, albums := albumTestServer(t, PermissionQueueAdd)
	view := postCommand(t, server, fmt.Sprintf(`{"action":"queue_album","album_key":%q}`, albums[0].Key))
	if len(view.Queue) != 2 {
		t.Fatalf("queue = %#v, want both album tracks", view.Queue)
	}
	if view.Queue[0].Track == nil || view.Queue[0].Track.Title != "Karmacoma" {
		t.Fatalf("queued order = %#v, want track-number order", view.Queue)
	}
}

func TestPlayingAnAlbumTwiceRestartsItWithoutDuplicatingTheQueue(t *testing.T) {
	server, albums := albumTestServer(t, PermissionQueueAdd, PermissionPlaybackControl)
	play := fmt.Sprintf(`{"action":"play_album","album_key":%q}`, albums[0].Key)

	view := postCommand(t, server, play)
	if len(view.Queue) != 1 || view.Current.Track.Title != "Karmacoma" {
		t.Fatalf("first play = %#v", view)
	}

	other, err := server.Library.Search(context.Background(), "Loose Ends")
	if err != nil || len(other) != 1 {
		t.Fatalf("other track = %#v, err = %v", other, err)
	}
	view = postCommand(t, server, fmt.Sprintf(`{"action":"queue_add","dedupe_key":%q}`, other[0].DedupeKey))
	if len(view.Queue) != 2 {
		t.Fatalf("queue after adding an unrelated track = %#v", view.Queue)
	}

	view = postCommand(t, server, play)
	if view.Current == nil || view.Current.Track == nil || view.Current.Track.Title != "Karmacoma" {
		t.Fatalf("restart did not play the album from its first track: %#v", view.Current)
	}
	if len(view.Queue) != 2 {
		t.Fatalf("restart changed the queue length to %d, want 2: %#v", len(view.Queue), view.Queue)
	}
	if view.Queue[0].Track.Title != "Better Things" || view.Queue[1].Track.Title != "Loose Ends" {
		t.Fatalf("restart reordered the queue: %#v", view.Queue)
	}
	if action := view.Actions[0].Text; !strings.Contains(action, "Restarted the album") {
		t.Fatalf("action = %q, want a restart entry", action)
	}
}

func TestQueueingAnAlbumThenPlayingItReplacesTheQueuedCopy(t *testing.T) {
	server, albums := albumTestServer(t, PermissionQueueAdd, PermissionPlaybackControl)
	view := postCommand(t, server, fmt.Sprintf(`{"action":"queue_album","album_key":%q}`, albums[0].Key))
	if len(view.Queue) != 2 {
		t.Fatalf("queued album = %#v", view.Queue)
	}
	view = postCommand(t, server, fmt.Sprintf(`{"action":"play_album","album_key":%q}`, albums[0].Key))
	if len(view.Queue) != 1 || view.Queue[0].Track.Title != "Better Things" {
		t.Fatalf("playing a queued album duplicated it: %#v", view.Queue)
	}
}

func TestRoomVolumeChangesAreRecordedAndCoalesced(t *testing.T) {
	server, _ := albumTestServer(t, PermissionVolumeControl)

	view := postCommand(t, server, `{"action":"room_audio","volume":0.4,"muted":false}`)
	if len(view.Actions) != 1 || view.Actions[0].Text != "Set the room volume to 80%." {
		t.Fatalf("actions after a volume change = %#v", view.Actions)
	}
	view = postCommand(t, server, `{"action":"room_audio","volume":0.1,"muted":false}`)
	if len(view.Actions) != 1 || view.Actions[0].Text != "Set the room volume to 20%." {
		t.Fatalf("successive volume changes should collapse: %#v", view.Actions)
	}
	view = postCommand(t, server, `{"action":"room_audio","volume":0.1,"muted":true}`)
	if len(view.Actions) != 2 || view.Actions[0].Text != "Muted the room." {
		t.Fatalf("mute should be its own entry: %#v", view.Actions)
	}
	view = postCommand(t, server, `{"action":"room_audio","volume":0.1,"muted":true}`)
	if len(view.Actions) != 2 {
		t.Fatalf("a no-op volume command should not log anything: %#v", view.Actions)
	}
}

func TestPlayAlbumRequiresPlaybackControl(t *testing.T) {
	server, albums := albumTestServer(t, PermissionQueueAdd)
	body := fmt.Sprintf(`{"action":"play_album","album_key":%q}`, albums[0].Key)
	req := httptest.NewRequest(http.MethodPost, "/rooms/main/api/command", strings.NewReader(body))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("play_album status = %d, want %d", rec.Code, http.StatusForbidden)
	}

	allowed, allowedAlbums := albumTestServer(t, PermissionQueueAdd, PermissionPlaybackControl)
	view := postCommand(t, allowed, fmt.Sprintf(`{"action":"play_album","album_key":%q}`, allowedAlbums[0].Key))
	if view.Current == nil || view.Current.Track == nil ||
		view.Current.Track.Title != "Karmacoma" || len(view.Queue) != 1 {
		t.Fatalf("play_album state = %#v", view)
	}
}

func TestLibraryRescanIsAvailableToEveryUser(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Artist - Song.mp3"), []byte("song"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{dir}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	server := testServer(&Server{
		Auth:    fakeAuth{user: UserInfo{Username: "alice"}},
		Library: lib,
	})
	defer server.Rooms.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/library/rescan", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("rescan status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	var accepted struct {
		Restarted bool `json:"restarted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.Restarted {
		t.Fatal("restarted = true, want false when no scan is running")
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		count, err := lib.Count(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("background rescan did not index the library")
}

func TestLibraryReportsWhoMayRestartAScan(t *testing.T) {
	ctx := context.Background()
	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{t.TempDir()}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()

	for _, testCase := range []struct {
		name string
		user UserInfo
		want bool
	}{
		{name: "listener", user: UserInfo{Username: "alice"}, want: false},
		{name: "admin", user: UserInfo{Username: "root", Role: RoleAdmin}, want: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := testServer(&Server{Auth: fakeAuth{user: testCase.user}, Library: lib})
			defer server.Rooms.Close()
			req := httptest.NewRequest(http.MethodGet, "/api/library", nil)
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, req)
			var info struct {
				CanRestartScan bool `json:"can_restart_scan"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
				t.Fatal(err)
			}
			if info.CanRestartScan != testCase.want {
				t.Fatalf("can_restart_scan = %v, want %v", info.CanRestartScan, testCase.want)
			}
		})
	}
}

func TestLibraryRescanRejectsSignedOutUsers(t *testing.T) {
	server := testServer(&Server{Auth: fakeAuth{}})
	defer server.Rooms.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/library/rescan", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous rescan status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func id3v23Frames(frames map[string]string) []byte {
	var body []byte
	for id, text := range frames {
		payload := append([]byte{0}, []byte(text)...)
		frame := []byte{id[0], id[1], id[2], id[3], byte(len(payload) >> 24), byte(len(payload) >> 16), byte(len(payload) >> 8), byte(len(payload)), 0, 0}
		body = append(body, append(frame, payload...)...)
	}
	size := len(body)
	return append([]byte{'I', 'D', '3', 3, 0, 0, byte(size >> 21), byte(size >> 14), byte(size >> 7), byte(size)}, body...)
}
