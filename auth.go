package main

import (
	"net/http"

	appauth "listen-party/internal/auth"
)

type Role = appauth.Role
type UserInfo = appauth.UserInfo
type UserSummary = appauth.UserSummary

const (
	RoleAdmin = appauth.RoleAdmin
)

type AuthGate interface {
	Authorized(r *http.Request, roles ...Role) bool
	CurrentUser(r *http.Request) (UserInfo, bool)
	ListEnabledUsers() ([]UserSummary, error)
	Require(roles ...Role) func(http.Handler) http.Handler
}
