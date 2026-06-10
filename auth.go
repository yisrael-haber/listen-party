package main

import (
	"crypto/subtle"
	"net/http"
	"sync"
)

type Role string

const (
	RoleListener Role = "listener"
	RoleAdmin    Role = "admin"
)

type BasicAuth struct {
	mu    sync.RWMutex
	creds map[Role]Credentials
}

func NewBasicAuth(cfg AuthConfig) *BasicAuth {
	b := &BasicAuth{}
	b.Update(cfg)
	return b
}

func (b *BasicAuth) Update(cfg AuthConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.creds = map[Role]Credentials{
		RoleListener: cfg.Listener,
		RoleAdmin:    cfg.Admin,
	}
}

func (b *BasicAuth) Authorized(r *http.Request, roles ...Role) bool {
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, role := range roles {
		creds := b.creds[role]
		if constantEqual(user, creds.Username) && constantEqual(pass, creds.Password) {
			return true
		}
	}
	return false
}

func (b *BasicAuth) Require(roles ...Role) func(http.Handler) http.Handler {
	return b.RequireRealm("listen-party", roles...)
}

func (b *BasicAuth) RequireRealm(realm string, roles ...Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !b.Authorized(r, roles...) {
				w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func constantEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
