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
	server := NewServer(ServerOptions{Auth: auth}).Handler()

	for _, path := range []string{"/admin", "/admin/", "/admin.js", "/api/admin/config"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("default credentials %s status = %d, want %d", path, rec.Code, http.StatusUnauthorized)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	server = NewServer(ServerOptions{Auth: fakeAuth{roles: []Role{RoleAdmin}}}).Handler()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin /admin status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHandlerMountsAuthRoutesBesideAppAPI(t *testing.T) {
	server := NewServer(ServerOptions{
		Auth:       fakeAuth{roles: []Role{RoleListener}},
		AuthRoutes: http.NotFoundHandler(),
	})
	_ = server.Handler()
}

func TestListenerStaticAssetsAreServed(t *testing.T) {
	server := NewServer(ServerOptions{Auth: fakeAuth{roles: []Role{RoleListener}}}).Handler()
	for _, path := range []string{"/", "/assets/style.css", "/assets/app.js"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", path, rec.Code, http.StatusOK)
		}
	}
}

func TestMeReturnsCurrentUser(t *testing.T) {
	server := NewServer(ServerOptions{
		Auth: fakeAuth{
			roles: []Role{RoleListener},
			user: UserInfo{
				ID:       "user1",
				Username: "first_user",
				Role:     RoleListener,
			},
		},
	}).Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/me status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); !strings.Contains(got, `"username":"first_user"`) {
		t.Fatalf("/api/me body = %s", got)
	}
}

func TestRoomsEndpointFiltersByUserAccess(t *testing.T) {
	server := NewServer(ServerOptions{
		Auth: fakeAuth{
			roles: []Role{RoleListener},
			user: UserInfo{
				ID:       "user1",
				Username: "alice",
				Role:     RoleListener,
				Groups:   []string{"staff"},
			},
		},
		Config: Config{Rooms: []RoomConfig{
			{ID: "public", Name: "Public Room", Public: true},
			{ID: "staff", Name: "Staff", AllowedGroups: []string{"staff"}},
			{ID: "private", Name: "Private"},
		}},
	}).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/rooms", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/rooms status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	for _, want := range []string{`"id":"public"`, `"id":"staff"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("/api/rooms body = %s, missing %s", body, want)
		}
	}
	if strings.Contains(body, `"id":"private"`) {
		t.Fatalf("/api/rooms body leaked private room: %s", body)
	}
}

func TestPrivateRoomPageRequiresRoomAccess(t *testing.T) {
	server := NewServer(ServerOptions{
		Auth: fakeAuth{
			roles: []Role{RoleListener},
			user: UserInfo{
				ID:       "user1",
				Username: "alice",
				Role:     RoleListener,
			},
		},
		Config: Config{Rooms: []RoomConfig{
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

type fakeAuth struct {
	roles []Role
	user  UserInfo
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
