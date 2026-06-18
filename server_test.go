package main

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestAdminPageRequiresAdminCredentials(t *testing.T) {
	auth := fakeAuth{roles: []Role{RoleListener}}
	server := testServer(&Server{Auth: auth}).Handler()

	for _, path := range []string{"/admin", "/admin.js", "/api/admin/config"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("default credentials %s status = %d, want %d", path, rec.Code, http.StatusUnauthorized)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	server = testServer(&Server{Auth: fakeAuth{roles: []Role{RoleAdmin}}}).Handler()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin /admin status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestListenerStaticAssetsAreServed(t *testing.T) {
	server := testServer(&Server{Auth: fakeAuth{roles: []Role{RoleListener}}}).Handler()
	for _, path := range []string{"/", "/assets/style.css", "/assets/app.js"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", path, rec.Code, http.StatusOK)
		}
	}
}

func TestSessionReturnsCurrentUserAndAccessibleRooms(t *testing.T) {
	server := testServer(&Server{
		Auth: fakeAuth{
			roles: []Role{RoleListener},
			user: UserInfo{
				ID:       "user1",
				Username: "alice",
				Role:     RoleListener,
				Groups:   []string{"staff"},
			},
		},
		Config: Config{Rooms: []Room{
			{ID: "public", Name: "Public Room", Public: true},
			{ID: "staff", Name: "Staff", AllowedGroups: []string{"staff"}},
			{ID: "private", Name: "Private"},
		}},
	}).Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/session status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	for _, want := range []string{`"username":"alice"`, `"id":"public"`, `"id":"staff"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("/api/session body = %s, missing %s", body, want)
		}
	}
	if strings.Contains(body, `"id":"private"`) {
		t.Fatalf("/api/session body leaked private room: %s", body)
	}
}

func TestPrivateRoomPageRequiresRoomAccess(t *testing.T) {
	server := testServer(&Server{
		Auth: fakeAuth{
			roles: []Role{RoleListener},
			user: UserInfo{
				ID:       "user1",
				Username: "alice",
				Role:     RoleListener,
			},
		},
		Config: Config{Rooms: []Room{
			{ID: "public", Name: "Public Room", Public: true},
			{ID: "private", Name: "Private"},
		}},
	}).Handler()

	req := httptest.NewRequest(http.MethodGet, "/rooms/private", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("/rooms/private status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestBannedIPIsRejectedButHealthzPasses(t *testing.T) {
	server := testServer(&Server{
		Auth:   fakeAuth{roles: []Role{RoleListener}},
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
		Auth:   fakeAuth{roles: []Role{RoleAdmin}},
		Config: Config{MusicDirs: []string{"/music/allowed"}},
	}).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/admin/rescan-dir", strings.NewReader(`{"music_dir":"/music/other"}`))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("/api/admin/rescan-dir status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestQueueMoveRequiresDirection(t *testing.T) {
	server := testServer(&Server{
		Auth: fakeAuth{
			roles: []Role{RoleListener},
			user: UserInfo{
				ID:       "user1",
				Username: "alice",
				Role:     RoleListener,
			},
		},
	}).Handler()

	req := httptest.NewRequest(http.MethodPost, "/rooms/public/api/command", strings.NewReader(`{"action":"queue_move","id":1}`))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("/queue/move status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

type fakeAuth struct {
	roles []Role
	user  UserInfo
}

func testServer(s *Server) *Server {
	if s.Rooms == nil {
		rooms := s.Config.Rooms
		if len(rooms) == 0 {
			rooms = []Room{{ID: defaultRoomID, Name: "Public Room", Public: true}}
		}
		s.Rooms = NewRoomManager(rooms)
	}
	return s
}

func (f fakeAuth) Authorized(_ *http.Request, roles ...Role) bool {
	for _, role := range roles {
		if slices.Contains(f.roles, role) {
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
