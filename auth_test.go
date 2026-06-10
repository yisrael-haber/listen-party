package main

import (
	"net/http/httptest"
	"testing"
)

func TestBasicAuthRoles(t *testing.T) {
	b := NewBasicAuth(AuthConfig{
		Listener: Credentials{Username: "listener", Password: "listen"},
		Admin:    Credentials{Username: "admin", Password: "admin"},
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("admin", "admin")
	if !b.Authorized(req, RoleAdmin) {
		t.Fatal("admin credentials rejected")
	}
	b.Update(AuthConfig{
		Listener: Credentials{Username: "new-listener", Password: "listen"},
		Admin:    Credentials{Username: "new-admin", Password: "admin"},
	})
	req.SetBasicAuth("new-admin", "admin")
	if !b.Authorized(req, RoleAdmin) {
		t.Fatal("updated admin credentials rejected")
	}
}
