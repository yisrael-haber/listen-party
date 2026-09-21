package server

import (
	"net/http"

	assets "listen-party"
	"listen-party/backend/app/commands"
	"listen-party/backend/app/configuration"
	"listen-party/backend/app/events"
	"listen-party/backend/app/media"
	"listen-party/backend/app/playlists"
	"listen-party/backend/app/roomadmin"
	"listen-party/backend/app/session"
	appauth "listen-party/backend/auth"
)

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	requireAdmin := s.Auth.Require(appauth.RoleAdmin)
	mux.Handle("GET /admin", requireAdmin(http.HandlerFunc(session.HandleAdminPage)))
	mux.Handle("GET /admin.js", requireAdmin(http.HandlerFunc(session.HandleAdminJS)))
	requireUser := s.Auth.Require()
	webFiles := http.FileServer(http.FS(assets.WebRoot()))
	adminFiles := requireAdmin(http.FileServer(http.FS(assets.AdminRoot())))
	mux.Handle("GET /admin/", adminFiles)
	mux.Handle("GET /{$}", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { session.HandleApp(w, r, s) })))
	mux.Handle("GET /favicon.ico", http.HandlerFunc(session.HandleFavicon))
	mux.Handle("GET /rooms/{room}", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { session.HandleApp(w, r, s) })))
	mux.Handle("GET /assets/", requireUser(http.StripPrefix("/assets/", webFiles)))
	mux.Handle("GET /rooms/{room}/events", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { events.Handle(w, r, s) })))
	mux.Handle("GET /api/session", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { session.HandleSession(w, r, s) })))
	mux.Handle("GET /rooms/{room}/api/state", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { media.HandleState(w, r, s) })))
	mux.Handle("GET /rooms/{room}/api/admin", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { roomadmin.Handle(w, r, s) })))
	mux.Handle("PUT /rooms/{room}/api/admin", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { roomadmin.HandleUpdate(w, r, s) })))
	mux.Handle("POST /rooms/{room}/api/admin/disconnect", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { roomadmin.HandleDisconnect(w, r, s) })))
	mux.Handle("GET /api/search", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { media.HandleSearch(w, r, s) })))
	mux.Handle("GET /api/library", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { media.HandleLibrary(w, r, s) })))
	mux.Handle("GET /api/playlists", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { playlists.HandlePlaylists(w, r, s) })))
	mux.Handle("POST /api/playlists", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { playlists.HandlePlaylistCreate(w, r, s) })))
	mux.Handle("GET /api/playlists/{id}", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { playlists.HandlePlaylist(w, r, s) })))
	mux.Handle("GET /api/playlists/{id}/export", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { playlists.HandlePlaylistExport(w, r, s) })))
	mux.Handle("DELETE /api/playlists/{id}", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { playlists.HandlePlaylistDelete(w, r, s) })))
	mux.Handle("POST /api/playlists/{id}/items", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { playlists.HandlePlaylistAddItem(w, r, s) })))
	mux.Handle("POST /api/playlists/{id}/import-folder", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { playlists.HandlePlaylistImportFolder(w, r, s) })))
	mux.Handle("POST /api/playlists/{id}/import", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { playlists.HandlePlaylistImport(w, r, s) })))
	mux.Handle("DELETE /api/playlists/{id}/items/{item}", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { playlists.HandlePlaylistRemoveItem(w, r, s) })))
	mux.Handle("POST /rooms/{room}/api/command", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { commands.Handle(w, r, s) })))
	mux.Handle("POST /api/admin/rescan", requireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { media.HandleRescan(w, r, s) })))
	mux.Handle("POST /api/admin/rescan-dir", requireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { media.HandleRescanDir(w, r, s) })))
	mux.Handle("GET /api/admin/config", requireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		configuration.Handle(w, r, s)
	})))
	mux.Handle("PUT /api/admin/config", requireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		configuration.HandleUpdate(w, r, s)
	})))
	mux.Handle("GET /media/{id}/artwork", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { media.HandleArtwork(w, r, s) })))
	mux.Handle("GET /media/{id}", requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { media.HandleMedia(w, r, s) })))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	if s.AuthRoutes == nil {
		return s.rejectBannedIPs(mux)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAuthRoute(r.URL.Path) {
			s.AuthRoutes.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
	return s.rejectBannedIPs(handler)
}
