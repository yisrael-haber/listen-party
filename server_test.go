package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminPageRequiresAdminCredentials(t *testing.T) {
	auth := NewBasicAuth(AuthConfig{
		Listener: Credentials{Username: "default", Password: "default"},
		Admin:    Credentials{Username: "admin", Password: "admin"},
	})
	server := NewServer(ServerOptions{Auth: auth}).Handler()

	for _, path := range []string{"/admin", "/admin/", "/admin.js", "/api/admin/config"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.SetBasicAuth("default", "default")
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("default credentials %s status = %d, want %d", path, rec.Code, http.StatusUnauthorized)
		}
		if got := rec.Header().Get("WWW-Authenticate"); got != `Basic realm="listen-party-admin"` {
			t.Fatalf("default credentials %s realm = %q, want admin realm", path, got)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.SetBasicAuth("admin", "admin")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin /admin status = %d, want %d", rec.Code, http.StatusOK)
	}
}
