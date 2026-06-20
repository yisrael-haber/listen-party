package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	musiclib "listen-party/internal/library"
)

func TestAdminPageRequiresAdminCredentials(t *testing.T) {
	server := testServer(&Server{Auth: fakeAuth{user: UserInfo{Username: "alice"}}}).Handler()
	for _, path := range []string{"/admin", "/admin.js", "/api/admin/config"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("regular user %s status = %d, want %d", path, rec.Code, http.StatusUnauthorized)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	server = testServer(&Server{Auth: fakeAuth{user: UserInfo{Username: "admin", Role: RoleAdmin}}}).Handler()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin /admin status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestEnabledUserStaticAssetsAreServed(t *testing.T) {
	server := testServer(&Server{Auth: fakeAuth{user: UserInfo{Username: "alice"}}}).Handler()
	for _, path := range []string{"/", "/assets/style.css", "/assets/app.js", "/assets/vendor/sortable-1.15.7.min.js"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", path, rec.Code, http.StatusOK)
		}
	}
}

func TestSessionReturnsEveryRoomAndEffectivePermissions(t *testing.T) {
	server := testServer(&Server{
		Auth: fakeAuth{user: UserInfo{ID: "user1", Username: "alice", Groups: []string{"staff"}}},
		Config: Config{Rooms: []Room{
			{ID: "main", Name: "Main Room"},
			{ID: "staff", Name: "Staff", Grants: map[string][]RoomPermission{
				"staff": {PermissionQueueManage},
			}},
			{ID: "quiet", Name: "Quiet"},
		}},
	}).Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/session status = %d, want %d", rec.Code, http.StatusOK)
	}
	var response struct {
		Rooms       []Room                      `json:"rooms"`
		Permissions map[string][]RoomPermission `json:"permissions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Rooms) != 3 {
		t.Fatalf("rooms = %#v, want all three rooms", response.Rooms)
	}
	if len(response.Permissions["staff"]) != 1 || response.Permissions["staff"][0] != PermissionQueueManage {
		t.Fatalf("staff permissions = %#v", response.Permissions["staff"])
	}
	if len(response.Permissions["quiet"]) != 0 {
		t.Fatalf("quiet permissions = %#v, want none", response.Permissions["quiet"])
	}
}

func TestEveryEnabledUserCanOpenEveryRoom(t *testing.T) {
	server := testServer(&Server{
		Auth:   fakeAuth{user: UserInfo{Username: "alice"}},
		Config: Config{Rooms: []Room{{ID: "quiet", Name: "Quiet"}}},
	}).Handler()
	req := httptest.NewRequest(http.MethodGet, "/rooms/quiet", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/rooms/quiet status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRoomCommandRequiresItsPermission(t *testing.T) {
	server := testServer(&Server{
		Auth: fakeAuth{user: UserInfo{Username: "alice", Groups: []string{"staff"}}},
		Config: Config{Rooms: []Room{{
			ID: "main", Name: "Main Room",
			Grants: map[string][]RoomPermission{"staff": {PermissionPlaybackControl}},
		}}},
	}).Handler()
	req := httptest.NewRequest(http.MethodPost, "/rooms/main/api/command", strings.NewReader(`{"action":"queue_add","dedupe_key":"track"}`))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("queue_add status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestStateUsesCurrentRoomPermissions(t *testing.T) {
	ctx := context.Background()
	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	server := testServer(&Server{
		Auth:    fakeAuth{user: UserInfo{Username: "alice", Groups: []string{"staff"}}},
		Library: lib,
		Config: Config{Rooms: []Room{{
			ID: "main", Name: "Main Room",
			Grants: map[string][]RoomPermission{"staff": {PermissionQueueManage}},
		}}},
	})

	permissions := getStatePermissions(t, server.Handler())
	if len(permissions) != 1 || permissions[0] != PermissionQueueManage {
		t.Fatalf("initial permissions = %#v", permissions)
	}
	server.Rooms.Update([]Room{{ID: "main", Name: "Main Room"}})
	if permissions := getStatePermissions(t, server.Handler()); len(permissions) != 0 {
		t.Fatalf("permissions after update = %#v, want none", permissions)
	}
}

func TestQueueRejectsUnknownTrack(t *testing.T) {
	ctx := context.Background()
	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	server := queueTestServer(lib).Handler()
	req := httptest.NewRequest(http.MethodPost, "/rooms/main/api/command", strings.NewReader(`{"action":"queue_add","dedupe_key":"missing"}`))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown track status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestQueueRejectsUnknownPlaylist(t *testing.T) {
	ctx := context.Background()
	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	server := queueTestServer(lib).Handler()
	req := httptest.NewRequest(http.MethodPost, "/rooms/main/api/command", strings.NewReader(`{"action":"playlist_queue","playlist_id":999}`))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown playlist status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestQueueReorderRequiresQueueItemIDAfterPermissionCheck(t *testing.T) {
	server := testServer(&Server{
		Auth: fakeAuth{user: UserInfo{Username: "alice", Groups: []string{"staff"}}},
		Config: Config{Rooms: []Room{{
			ID: "main", Name: "Main Room",
			Grants: map[string][]RoomPermission{"staff": {PermissionQueueManage}},
		}}},
	}).Handler()
	req := httptest.NewRequest(http.MethodPost, "/rooms/main/api/command", strings.NewReader(`{"action":"queue_reorder"}`))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("queue_reorder status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestQueueReorderUsesStableQueueItemIDs(t *testing.T) {
	ctx := context.Background()
	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	server := testServer(&Server{
		Auth:    fakeAuth{user: UserInfo{Username: "alice", Groups: []string{"staff"}}},
		Library: lib,
		Config: Config{Rooms: []Room{{
			ID: "main", Name: "Main Room",
			Grants: map[string][]RoomPermission{"staff": {PermissionQueueManage}},
		}}},
	})
	room, _ := server.Rooms.Get("main")
	room.Playback.Add("10", "alice")
	room.Playback.Add("20", "alice")
	state := room.Playback.Add("30", "alice")
	body := fmt.Sprintf(`{"action":"queue_reorder","queue_item_id":%d,"before_queue_item_id":%d}`, state.Queue[2].ID, state.Queue[0].ID)

	req := httptest.NewRequest(http.MethodPost, "/rooms/main/api/command", strings.NewReader(body))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("queue_reorder status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	state = room.Playback.Snapshot()
	if got := []string{state.Queue[0].DedupeKey, state.Queue[1].DedupeKey, state.Queue[2].DedupeKey}; !slices.Equal(got, []string{"30", "10", "20"}) {
		t.Fatalf("reordered queue = %v, want [30 10 20]", got)
	}
}

func TestPermissionForActionKeepsCapabilitiesIndependent(t *testing.T) {
	for _, action := range []string{"queue_add", "playlist_queue", "playlist_shuffle"} {
		if permission, ok := permissionForAction(action); !ok || permission != PermissionQueueAdd {
			t.Fatalf("%s permission = %q, %v", action, permission, ok)
		}
	}
	for _, action := range []string{"queue_remove", "queue_reorder", "queue_clear", "history_clear"} {
		if permission, ok := permissionForAction(action); !ok || permission != PermissionQueueManage {
			t.Fatalf("%s permission = %q, %v", action, permission, ok)
		}
	}
	for _, action := range []string{"play", "play_now", "pause", "previous", "seek", "skip"} {
		if permission, ok := permissionForAction(action); !ok || permission != PermissionPlaybackControl {
			t.Fatalf("%s permission = %q, %v", action, permission, ok)
		}
	}
}

func TestEveryPlaylistIsVisible(t *testing.T) {
	ctx := context.Background()
	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), []string{t.TempDir()}, 1)
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	defer lib.Close()
	if _, err := lib.CreatePlaylist(ctx, "First", "owner1"); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.CreatePlaylist(ctx, "Second", "owner1"); err != nil {
		t.Fatal(err)
	}

	server := testServer(&Server{Auth: fakeAuth{user: UserInfo{ID: "other", Username: "bob"}}, Library: lib}).Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/playlists", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/playlists status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	for _, name := range []string{"First", "Second"} {
		if !strings.Contains(body, `"name":"`+name+`"`) {
			t.Fatalf("/api/playlists body = %s, missing %s", body, name)
		}
	}
}

func TestPlaylistDeleteRequiresOwnerOrAdmin(t *testing.T) {
	ctx := context.Background()
	lib, err := musiclib.Open(ctx, filepath.Join(t.TempDir(), "tracks.sqlite"), nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	playlist, err := lib.CreatePlaylist(ctx, "Owner playlist", "owner1")
	if err != nil {
		t.Fatal(err)
	}

	request := func(user UserInfo, playlistID int64) *httptest.ResponseRecorder {
		server := testServer(&Server{Auth: fakeAuth{user: user}, Library: lib}).Handler()
		req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/playlists/%d", playlistID), nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		return rec
	}

	if rec := request(UserInfo{ID: "other", Username: "other"}, playlist.ID); rec.Code != http.StatusForbidden {
		t.Fatalf("unrelated delete status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if rec := request(UserInfo{ID: "owner1", Username: "owner"}, playlist.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("owner delete status = %d, want %d: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}

	playlist, err = lib.CreatePlaylist(ctx, "Admin playlist", "owner1")
	if err != nil {
		t.Fatal(err)
	}
	admin := UserInfo{ID: "admin1", Username: "admin", Role: RoleAdmin}
	if rec := request(admin, playlist.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete status = %d, want %d: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
}

func TestBannedIPIsRejectedButHealthzPasses(t *testing.T) {
	server := testServer(&Server{
		Auth:   fakeAuth{user: UserInfo{Username: "alice"}},
		Config: Config{BannedIPs: []string{"192.168.1.50"}},
	}).Handler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.50:12345"
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("banned / status = %d, want %d", rec.Code, http.StatusForbidden)
	}

	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "192.168.1.50:12345"
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("banned /healthz status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestRescanDirRequiresConfiguredMusicDir(t *testing.T) {
	server := testServer(&Server{
		Auth:   fakeAuth{user: UserInfo{Username: "admin", Role: RoleAdmin}},
		Config: Config{MusicDirs: []string{"/music/allowed"}},
	}).Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/rescan-dir", strings.NewReader(`{"music_dir":"/music/other"}`))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("/api/admin/rescan-dir status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

type fakeAuth struct {
	user UserInfo
}

func getStatePermissions(t *testing.T, handler http.Handler) []RoomPermission {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/rooms/main/api/state", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("state status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var state ViewState
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	return state.Permissions
}

func queueTestServer(lib *musiclib.Library) *Server {
	return testServer(&Server{
		Auth:    fakeAuth{user: UserInfo{Username: "alice", Groups: []string{"staff"}}},
		Library: lib,
		Config: Config{Rooms: []Room{{
			ID: "main", Name: "Main Room",
			Grants: map[string][]RoomPermission{"staff": {PermissionQueueAdd}},
		}}},
	})
}

func testServer(s *Server) *Server {
	if s.Rooms == nil {
		rooms := s.Config.Rooms
		if len(rooms) == 0 {
			rooms = []Room{{ID: defaultRoomID, Name: "Main Room"}}
		}
		s.Rooms = NewRoomManager(rooms)
	}
	return s
}

func (f fakeAuth) Authorized(_ *http.Request, roles ...Role) bool {
	if f.user.Username == "" {
		return false
	}
	if len(roles) == 0 {
		return true
	}
	for _, role := range roles {
		if role == RoleAdmin && f.user.Role == RoleAdmin {
			return true
		}
	}
	return false
}

func (f fakeAuth) CurrentUser(_ *http.Request) (UserInfo, bool) {
	if f.user.Username == "" {
		return UserInfo{}, false
	}
	return f.user, true
}

func (f fakeAuth) Require(roles ...Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !f.Authorized(r, roles...) {
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
