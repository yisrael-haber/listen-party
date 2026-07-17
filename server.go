package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	musiclib "listen-party/internal/library"
)

type Server struct {
	Auth           AuthGate
	AuthRoutes     http.Handler
	Library        *musiclib.Library
	Rooms          *RoomManager
	Config         Config
	ConfigPath     string
	configMu       sync.RWMutex
	configUpdateMu sync.Mutex
}

const maxFolderImportFiles = 50_000
const maxRoomVolume = 0.5

var errAutoDJConfigurationChanged = errors.New("auto-dj configuration changed")

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	requireAdmin := s.Auth.Require(RoleAdmin)
	mux.Handle("GET /admin", requireAdmin(http.HandlerFunc(s.handleAdminPage)))
	mux.Handle("GET /admin.js", requireAdmin(http.HandlerFunc(s.handleAdminJS)))
	requireUser := s.Auth.Require()
	webFiles := http.FileServer(http.FS(webRoot()))
	adminFiles := requireAdmin(http.FileServer(http.FS(adminRoot())))
	mux.Handle("GET /admin/", adminFiles)
	mux.Handle("GET /{$}", requireUser(http.HandlerFunc(s.handleApp)))
	mux.Handle("GET /rooms/{room}", requireUser(http.HandlerFunc(s.handleApp)))
	mux.Handle("GET /assets/", requireUser(http.StripPrefix("/assets/", webFiles)))
	mux.Handle("GET /rooms/{room}/events", requireUser(http.HandlerFunc(s.handleEvents)))
	mux.Handle("GET /api/session", requireUser(http.HandlerFunc(s.handleSession)))
	mux.Handle("GET /rooms/{room}/api/state", requireUser(http.HandlerFunc(s.handleState)))
	mux.Handle("GET /rooms/{room}/api/admin", requireUser(http.HandlerFunc(s.handleRoomAdmin)))
	mux.Handle("PUT /rooms/{room}/api/admin/grants", requireUser(http.HandlerFunc(s.handleRoomAdminGrants)))
	mux.Handle("POST /rooms/{room}/api/admin/disconnect", requireUser(http.HandlerFunc(s.handleRoomAdminDisconnect)))
	mux.Handle("GET /api/search", requireUser(http.HandlerFunc(s.handleSearch)))
	mux.Handle("GET /api/library", requireUser(http.HandlerFunc(s.handleLibrary)))
	mux.Handle("GET /api/playlists", requireUser(http.HandlerFunc(s.handlePlaylists)))
	mux.Handle("POST /api/playlists", requireUser(http.HandlerFunc(s.handlePlaylistCreate)))
	mux.Handle("GET /api/playlists/{id}", requireUser(http.HandlerFunc(s.handlePlaylist)))
	mux.Handle("DELETE /api/playlists/{id}", requireUser(http.HandlerFunc(s.handlePlaylistDelete)))
	mux.Handle("POST /api/playlists/{id}/items", requireUser(http.HandlerFunc(s.handlePlaylistAddItem)))
	mux.Handle("POST /api/playlists/{id}/import-folder", requireUser(http.HandlerFunc(s.handlePlaylistImportFolder)))
	mux.Handle("DELETE /api/playlists/{id}/items/{item}", requireUser(http.HandlerFunc(s.handlePlaylistRemoveItem)))
	mux.Handle("POST /rooms/{room}/api/command", requireUser(http.HandlerFunc(s.handleCommand)))
	mux.Handle("POST /api/admin/rescan", requireAdmin(http.HandlerFunc(s.handleRescan)))
	mux.Handle("POST /api/admin/rescan-dir", requireAdmin(http.HandlerFunc(s.handleRescanDir)))
	mux.Handle("GET /api/admin/config", requireAdmin(http.HandlerFunc(s.handleConfig)))
	mux.Handle("PUT /api/admin/config", requireAdmin(http.HandlerFunc(s.handleConfigUpdate)))
	mux.Handle("GET /media/{id}/artwork", requireUser(http.HandlerFunc(s.handleArtwork)))
	mux.Handle("GET /media/{id}", requireUser(http.HandlerFunc(s.handleMedia)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
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

func (s *Server) rejectBannedIPs(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		ip, ok := clientIP(r.RemoteAddr)
		if ok && s.ipIsBanned(ip) {
			slog.Warn("blocked banned ip", "remote", r.RemoteAddr, "ip", ip.String(), "path", r.URL.Path)
			http.Error(w, "You are not allowed to access this resource, and have been banned due to suspicious activity.", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ipIsBanned(ip netip.Addr) bool {
	s.configMu.RLock()
	banned := append([]string(nil), s.Config.BannedIPs...)
	s.configMu.RUnlock()
	for _, value := range banned {
		bannedIP, err := netip.ParseAddr(value)
		if err == nil && bannedIP == ip {
			return true
		}
	}
	return false
}

func clientIP(remoteAddr string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = strings.Trim(remoteAddr, "[]")
	}
	ip, err := netip.ParseAddr(host)
	return ip, err == nil
}

func isAuthRoute(path string) bool {
	for _, prefix := range []string{
		"/login",
		"/logout",
		"/authAdmin",
		"/_",
		"/api/backups",
		"/api/batch",
		"/api/collections",
		"/api/crons",
		"/api/files",
		"/api/health",
		"/api/logs",
		"/api/oauth2-redirect",
		"/api/realtime",
		"/api/settings",
		"/api/sql",
	} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func (s *Server) handleApp(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("room") != "" {
		if _, _, ok := s.roomFromRequest(w, r); !ok {
			return
		}
	}
	http.ServeFileFS(w, r, webRoot(), "index.html")
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	user, ok := s.Auth.CurrentUser(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	rooms := s.Rooms.List()
	type roomSummary struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	summaries := make([]roomSummary, 0, len(rooms))
	permissions := make(map[string][]RoomPermission, len(rooms))
	administration := make(map[string]bool, len(rooms))
	disconnected := make(map[string]bool, len(rooms))
	for _, room := range rooms {
		summaries = append(summaries, roomSummary{ID: room.ID, Name: room.Name})
		permissions[room.ID] = RoomPermissionsForUser(user, room)
		administration[room.ID] = UserIsRoomAdmin(user, room)
		if activeRoom, ok := s.Rooms.Get(room.ID); ok {
			disconnected[room.ID] = activeRoom.Playback.ListenerDisconnected(user)
		}
	}
	writeJSON(w, map[string]any{
		"default_room_id":     s.Rooms.DefaultID(),
		"rooms":               summaries,
		"permissions":         permissions,
		"room_administration": administration,
		"disconnected":        disconnected,
		"user":                user,
	})
}

func (s *Server) roomFromRequest(w http.ResponseWriter, r *http.Request) (*Room, UserInfo, bool) {
	roomID := r.PathValue("room")
	if roomID == "" {
		roomID = s.Rooms.DefaultID()
	}
	room, ok := s.Rooms.Get(roomID)
	if !ok {
		http.Error(w, "room not found", http.StatusNotFound)
		return nil, UserInfo{}, false
	}
	user, ok := s.Auth.CurrentUser(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return nil, UserInfo{}, false
	}
	return room, user, true
}

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	http.ServeFileFS(w, r, adminRoot(), "admin.html")
}

func (s *Server) handleAdminJS(w http.ResponseWriter, r *http.Request) {
	http.ServeFileFS(w, r, adminRoot(), "admin.js")
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	room, user, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	ch, cancel, allowed := room.Playback.SubscribeIfAllowed(user)
	if !allowed {
		http.Error(w, "session disconnected; sign in again", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	slog.Info("listener connected", "remote", r.RemoteAddr, "username", user.Username, "room", room.ID, "listener_count", len(room.Playback.Snapshot().Listeners))
	defer func() {
		cancel()
		slog.Info("listener disconnected", "remote", r.RemoteAddr, "username", user.Username, "room", room.ID, "listener_count", len(room.Playback.Snapshot().Listeners))
	}()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	lifetime := time.NewTimer(10 * time.Minute)
	defer lifetime.Stop()

	for {
		select {
		case <-r.Context().Done():
			slog.Info("listener request closed", "remote", r.RemoteAddr, "username", user.Username, "room", room.ID, "error", r.Context().Err())
			return
		case <-lifetime.C:
			slog.Info("listener refresh requested", "remote", r.RemoteAddr, "username", user.Username, "room", room.ID)
			return
		case state, ok := <-ch:
			if !ok {
				slog.Info("listener subscription closed", "remote", r.RemoteAddr, "username", user.Username, "room", room.ID)
				return
			}
			if !s.writeEvent(w, r, state) {
				slog.Info("listener sse write closed", "remote", r.RemoteAddr, "username", user.Username, "room", room.ID)
				return
			}
		case <-ticker.C:
			if !s.writeEvent(w, r, room.Playback.Snapshot()) {
				slog.Info("listener heartbeat write closed", "remote", r.RemoteAddr, "username", user.Username, "room", room.ID)
				return
			}
		}
	}
}

func (s *Server) writeEvent(w http.ResponseWriter, r *http.Request, state PlaybackState) bool {
	if state.Disconnect {
		if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			slog.Debug("set sse disconnect write deadline", "error", err)
		}
		if _, err := fmt.Fprint(w, "event: disconnect\ndata: {}\n\n"); err != nil {
			slog.Warn("write sse disconnect", "error", err)
			return false
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return true
	}
	payload, err := s.viewStateForRequest(r, state)
	if err != nil {
		slog.Warn("build sse state", "error", err)
		return false
	}
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		slog.Debug("set sse write deadline", "error", err)
	}
	fmt.Fprintf(w, "event: state\ndata: ")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Warn("write sse state", "error", err)
		return false
	}
	fmt.Fprint(w, "\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return true
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	room, user, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	if room.Playback.ListenerDisconnected(user) {
		http.Error(w, "session disconnected; sign in again", http.StatusConflict)
		return
	}
	state, err := s.viewStateForRequest(r, room.Playback.Snapshot())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, state)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	tracks, err := s.Library.SearchField(r.Context(), q, r.URL.Query().Get("field"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, tracks)
}

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	count, err := s.Library.Count(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, map[string]any{
		"track_count": count,
		"scan":        s.Library.ScanStatus(),
	})
}

type playlistView struct {
	musiclib.Playlist
	CanEdit bool `json:"can_edit"`
}

func (s *Server) handlePlaylists(w http.ResponseWriter, r *http.Request) {
	user, ok := s.Auth.CurrentUser(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	playlists, err := s.Library.ListPlaylists(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]playlistView, 0, len(playlists))
	for _, playlist := range playlists {
		out = append(out, s.playlistView(user, playlist))
	}
	writeJSON(w, out)
}

func (s *Server) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	user, ok := s.Auth.CurrentUser(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	playlist, err := s.Library.GetPlaylist(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, s.playlistView(user, playlist))
}

func (s *Server) handlePlaylistCreate(w http.ResponseWriter, r *http.Request) {
	user, ok := s.Auth.CurrentUser(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	playlist, err := s.Library.CreatePlaylist(r.Context(), req.Name, user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, s.playlistView(user, playlist))
}

func (s *Server) handlePlaylistAddItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.Auth.CurrentUser(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	playlist, err := s.Library.GetPlaylist(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	if !userCanEditPlaylist(user, playlist) {
		http.Error(w, "playlist edit denied", http.StatusForbidden)
		return
	}
	var req struct {
		DedupeKey string `json:"dedupe_key"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.DedupeKey == "" {
		http.Error(w, "dedupe_key is required", http.StatusBadRequest)
		return
	}
	if _, err := s.Library.AddPlaylistTrack(r.Context(), id, req.DedupeKey); err != nil {
		writeError(w, err)
		return
	}
	playlist, err = s.Library.GetPlaylist(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, s.playlistView(user, playlist))
}

func (s *Server) handlePlaylistRemoveItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.Auth.CurrentUser(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	itemID, ok := pathID(w, r, "item")
	if !ok {
		return
	}
	playlist, err := s.Library.GetPlaylist(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	if !userCanEditPlaylist(user, playlist) {
		http.Error(w, "playlist edit denied", http.StatusForbidden)
		return
	}
	if err := s.Library.RemovePlaylistItem(r.Context(), id, itemID); err != nil {
		writeError(w, err)
		return
	}
	s.Rooms.InvalidateAutoDJPlaylistCandidate(id)
	playlist, err = s.Library.GetPlaylist(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, s.playlistView(user, playlist))
}

func (s *Server) handlePlaylistImportFolder(w http.ResponseWriter, r *http.Request) {
	user, ok := s.Auth.CurrentUser(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	playlist, err := s.Library.GetPlaylist(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	if !userCanEditPlaylist(user, playlist) {
		http.Error(w, "playlist edit denied", http.StatusForbidden)
		return
	}
	var req struct {
		Files []musiclib.FolderManifestFile `json:"files"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Files) > maxFolderImportFiles {
		http.Error(w, "folder contains too many files", http.StatusRequestEntityTooLarge)
		return
	}
	result, err := s.Library.ImportPlaylistFolder(r.Context(), id, req.Files)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, result)
}

func (s *Server) handlePlaylistDelete(w http.ResponseWriter, r *http.Request) {
	user, ok := s.Auth.CurrentUser(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	playlist, err := s.Library.GetPlaylist(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	if !userCanEditPlaylist(user, playlist) {
		http.Error(w, "playlist edit denied", http.StatusForbidden)
		return
	}
	if err := s.Library.DeletePlaylist(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	s.Rooms.ResetAutoDJPlaylistSource(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) playlistView(user UserInfo, playlist musiclib.Playlist) playlistView {
	return playlistView{
		Playlist: playlist,
		CanEdit:  userCanEditPlaylist(user, playlist),
	}
}

func userCanEditPlaylist(user UserInfo, playlist musiclib.Playlist) bool {
	return user.Role == RoleAdmin || playlist.OwnerID == user.ID
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	cfg := s.Config
	s.configMu.RUnlock()
	writeJSON(w, cfg)
}

func (s *Server) handleConfigUpdate(w http.ResponseWriter, r *http.Request) {
	var cfg Config
	if !readJSON(w, r, &cfg) {
		return
	}
	s.configUpdateMu.Lock()
	defer s.configUpdateMu.Unlock()
	s.configMu.RLock()
	old := s.Config
	path := s.ConfigPath
	s.configMu.RUnlock()
	if cfg.Revision != old.Revision {
		http.Error(w, "configuration changed; reload before saving", http.StatusConflict)
		return
	}
	cfg.Revision = old.Revision + 1

	if err := cfg.ApplyDefaultsForRoot(filepath.Dir(path)); err != nil {
		slog.Warn("reject config update", "remote", r.RemoteAddr, "error", err)
		writeError(w, err)
		return
	}

	if err := SaveConfig(path, cfg); err != nil {
		slog.Warn("save config failed", "remote", r.RemoteAddr, "path", path, "error", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.Library.UpdateScanConfig(cfg.MusicDirs, cfg.ScanWorkers)
	for _, removedID := range removedRoomIDs(old.Rooms, cfg.Rooms) {
		if err := s.Library.DeleteRoomPlaybackSnapshot(r.Context(), removedID); err != nil {
			slog.Warn("delete removed room playback state", "room", removedID, "error", err)
		}
	}
	s.Rooms.Update(cfg.Rooms)

	s.configMu.Lock()
	s.Config = cfg
	s.configMu.Unlock()

	slog.Info("config updated",
		"remote", r.RemoteAddr,
		"path", path,
		"addr_changed", cfg.Addr != old.Addr,
		"auth_changed", cfg.Auth.PocketBase != old.Auth.PocketBase,
		"music_dirs", len(cfg.MusicDirs),
		"scan_workers", cfg.ScanWorkers,
	)
	writeJSON(w, cfg)
}

func (s *Server) handleRoomAdmin(w http.ResponseWriter, r *http.Request) {
	room, user, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	if !UserIsRoomAdmin(user, *room) {
		http.Error(w, "room administration denied", http.StatusForbidden)
		return
	}
	writeJSON(w, map[string]any{
		"id":     room.ID,
		"name":   room.Name,
		"grants": cloneRoomGrants(room.Grants),
	})
}

func (s *Server) handleRoomAdminGrants(w http.ResponseWriter, r *http.Request) {
	room, user, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	if !UserIsRoomAdmin(user, *room) {
		http.Error(w, "room administration denied", http.StatusForbidden)
		return
	}
	var req struct {
		Grants map[string][]RoomPermission `json:"grants"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	s.configUpdateMu.Lock()
	defer s.configUpdateMu.Unlock()

	s.configMu.RLock()
	cfg := cloneConfig(s.Config)
	configPath := s.ConfigPath
	s.configMu.RUnlock()
	found := false
	for i := range cfg.Rooms {
		if cfg.Rooms[i].ID == room.ID {
			if !UserIsRoomAdmin(user, cfg.Rooms[i]) {
				http.Error(w, "room administration denied", http.StatusForbidden)
				return
			}
			cfg.Rooms[i].Grants = normalizeRoomGrants(req.Grants)
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "room not found", http.StatusNotFound)
		return
	}
	cfg.Revision++
	if err := SaveConfig(configPath, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.Rooms.Update(cfg.Rooms)
	s.configMu.Lock()
	s.Config = cfg
	s.configMu.Unlock()
	updated, _ := s.Rooms.Get(room.ID)
	writeJSON(w, map[string]any{
		"id":     updated.ID,
		"name":   updated.Name,
		"grants": cloneRoomGrants(updated.Grants),
	})
}

func (s *Server) handleRoomAdminDisconnect(w http.ResponseWriter, r *http.Request) {
	room, user, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	if !UserIsRoomAdmin(user, *room) {
		http.Error(w, "room administration denied", http.StatusForbidden)
		return
	}
	var req struct {
		Username string `json:"username"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" {
		http.Error(w, "username is required", http.StatusBadRequest)
		return
	}
	if !room.Playback.DisconnectListener(req.Username) {
		http.Error(w, "listener not found", http.StatusNotFound)
		return
	}
	slog.Info("listener disconnected by room administrator", "room", room.ID, "username", req.Username, "administrator", user.Username)
	w.WriteHeader(http.StatusNoContent)
}

func cloneConfig(cfg Config) Config {
	cfg.MusicDirs = append([]string(nil), cfg.MusicDirs...)
	cfg.BannedIPs = append([]string(nil), cfg.BannedIPs...)
	cfg.Rooms = append([]Room(nil), cfg.Rooms...)
	for i := range cfg.Rooms {
		cfg.Rooms[i].AdminGroups = append([]string(nil), cfg.Rooms[i].AdminGroups...)
		cfg.Rooms[i].Grants = cloneRoomGrants(cfg.Rooms[i].Grants)
	}
	return cfg
}

func removedRoomIDs(oldRooms, newRooms []Room) []string {
	remaining := make(map[string]struct{}, len(newRooms))
	for _, room := range newRooms {
		remaining[room.ID] = struct{}{}
	}
	removed := make([]string, 0)
	for _, room := range oldRooms {
		if _, ok := remaining[room.ID]; !ok {
			removed = append(removed, room.ID)
		}
	}
	return removed
}

func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	room, user, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	var req struct {
		Action            string       `json:"action"`
		DedupeKey         string       `json:"dedupe_key"`
		QueueItemID       int64        `json:"queue_item_id"`
		BeforeQueueItemID int64        `json:"before_queue_item_id"`
		PositionMS        int64        `json:"position_ms"`
		Enabled           bool         `json:"enabled"`
		Source            AutoDJSource `json:"source"`
		Volume            float64      `json:"volume"`
		Muted             bool         `json:"muted"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	permission, known := permissionForAction(req.Action)
	if !known {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if !s.Rooms.UserHasPermission(room.ID, user, permission) {
		http.Error(w, "room permission denied", http.StatusForbidden)
		return
	}
	displayName := user.Display()
	switch req.Action {
	case "auto_dj":
		if !req.Enabled {
			s.writeCommandState(w, r, "auto_dj_disable", room, displayName, room.Playback.ConfigureAutoDJ(false, "", nil))
			return
		}
		config, _ := room.Playback.AutoDJConfiguration()
		candidate, entries, err := s.newAutoDJCycle(r.Context(), config.Source)
		if err != nil {
			writeError(w, err)
			return
		}
		state, configured := room.Playback.ConfigureAutoDJForSource(config.Source, true, candidate, entries)
		if !configured {
			http.Error(w, "shuffle source changed; retry", http.StatusConflict)
			return
		}
		s.writeCommandState(w, r, "auto_dj_enable", room, displayName, state)
	case "auto_dj_source":
		source, err := s.resolveAutoDJSource(r.Context(), req.Source)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		config, _ := room.Playback.AutoDJConfiguration()
		if !config.Enabled {
			var available bool
			if source.Type == AutoDJSourceLibrary {
				count, err := s.Library.Count(r.Context())
				if err != nil {
					writeError(w, err)
					return
				}
				available = count > 0
			} else {
				entries, err := s.Library.PlaylistShuffleItemIDs(r.Context(), source.PlaylistID)
				if err != nil {
					writeError(w, err)
					return
				}
				available = len(entries) > 0
			}
			if !available {
				http.Error(w, "shuffle source contains no available tracks", http.StatusConflict)
				return
			}
			s.writeCommandState(w, r, "auto_dj_source", room, displayName, room.Playback.ConfigureAutoDJSource(source, "", nil))
			return
		}
		candidate, entries, err := s.newAutoDJCycle(r.Context(), source)
		if err != nil {
			writeError(w, err)
			return
		}
		s.writeCommandState(w, r, "auto_dj_source", room, displayName, room.Playback.ConfigureAutoDJSource(source, candidate, entries))
	case "queue_add":
		if req.DedupeKey == "" {
			http.Error(w, "dedupe_key is required", http.StatusBadRequest)
			return
		}
		track, err := s.Library.ResolveDedupeKey(r.Context(), req.DedupeKey)
		if err != nil {
			writeError(w, err)
			return
		}
		if track.DurationMS <= 0 {
			s.Library.EnsureDuration(track.ID)
		}
		state, err := room.Playback.Add(req.DedupeKey, displayName)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		s.writeCommandState(w, r, "queue_add", room, displayName, state)
	case "queue_remove":
		if req.QueueItemID <= 0 {
			http.Error(w, "queue_item_id is required", http.StatusBadRequest)
			return
		}
		before := room.Playback.Snapshot()
		removed, ok := queueItemByID(before.Queue, req.QueueItemID)
		state := room.Playback.Remove(req.QueueItemID)
		if ok && len(state.Queue) != len(before.Queue) {
			state = s.recordRoomAction(r, room, displayName, fmt.Sprintf("Removed %q from the queue.", s.trackActionName(r.Context(), removed.DedupeKey)))
		}
		s.writeCommandState(w, r, "queue_remove", room, displayName, state)
	case "queue_reorder":
		if req.QueueItemID <= 0 {
			http.Error(w, "queue_item_id is required", http.StatusBadRequest)
			return
		}
		if req.BeforeQueueItemID < 0 {
			http.Error(w, "before_queue_item_id must not be negative", http.StatusBadRequest)
			return
		}
		before := room.Playback.Snapshot()
		moved, movedOK := queueItemByID(before.Queue, req.QueueItemID)
		target, targetOK := queueItemByID(before.Queue, req.BeforeQueueItemID)
		state, err := room.Playback.Reorder(req.QueueItemID, req.BeforeQueueItemID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if movedOK && queueOrderChanged(before.Queue, state.Queue) {
			movedName := s.trackActionName(r.Context(), moved.DedupeKey)
			if req.BeforeQueueItemID == 0 {
				state = s.recordRoomAction(r, room, displayName, fmt.Sprintf("Moved %q to the end of the queue.", movedName))
			} else if targetOK {
				state = s.recordRoomAction(r, room, displayName, fmt.Sprintf("Moved %q before %q in the queue.", movedName, s.trackActionName(r.Context(), target.DedupeKey)))
			}
		}
		s.writeCommandState(w, r, "queue_reorder", room, displayName, state)
	case "queue_clear":
		before := room.Playback.Snapshot()
		state := room.Playback.Clear()
		if len(before.Queue) > 0 {
			state = s.recordRoomAction(r, room, displayName, "Cleared the queue.")
		}
		s.writeCommandState(w, r, "queue_clear", room, displayName, state)
	case "play":
		state, err := room.Playback.Play()
		if err != nil {
			slog.Warn("play rejected", "remote", r.RemoteAddr, "room", room.ID, "error", err)
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		s.writeCommandState(w, r, "play", room, displayName, state)
	case "play_now":
		if req.DedupeKey == "" {
			http.Error(w, "dedupe_key is required", http.StatusBadRequest)
			return
		}
		track, err := s.Library.ResolveDedupeKey(r.Context(), req.DedupeKey)
		if err != nil {
			writeError(w, err)
			return
		}
		if track.DurationMS <= 0 {
			s.Library.EnsureDuration(track.ID)
		}
		before := room.Playback.Snapshot()
		state := room.Playback.PlayNow(req.DedupeKey, displayName)
		if before.Current.DedupeKey != "" {
			state = s.recordRoomAction(r, room, displayName, fmt.Sprintf("Played %q now, replacing %q.", trackActionTitle(track), s.trackActionName(r.Context(), before.Current.DedupeKey)))
		}
		s.writeCommandState(w, r, "play_now", room, displayName, state)
	case "pause":
		s.writeCommandState(w, r, "pause", room, displayName, room.Playback.Pause())
	case "room_audio":
		if req.Volume < 0 || req.Volume > maxRoomVolume {
			http.Error(w, "volume must be between 0 and 0.5", http.StatusBadRequest)
			return
		}
		s.writeCommandState(w, r, "room_audio", room, displayName, room.Playback.SetRoomAudio(req.Volume, req.Muted))
	case "previous":
		s.writeCommandState(w, r, "previous", room, displayName, room.Playback.Previous())
	case "seek":
		s.writeCommandState(w, r, "seek", room, displayName, room.Playback.SeekTo(req.PositionMS))
	case "skip":
		before := room.Playback.Snapshot()
		if err := s.prepareAutoDJ(r.Context(), room); err != nil {
			writeError(w, err)
			return
		}
		state := room.Playback.Skip()
		s.replenishAutoDJ(r.Context(), room)
		if before.Current.DedupeKey != "" {
			state = s.recordRoomAction(r, room, displayName, s.skipActionText(r.Context(), before.Current.DedupeKey, state.Current.DedupeKey))
		}
		s.writeCommandState(w, r, "skip", room, displayName, state)
	case "history_clear":
		s.writeCommandState(w, r, "history_clear", room, displayName, room.Playback.ClearHistory())
	}
}

func queueItemByID(queue []PlaybackItem, id int64) (PlaybackItem, bool) {
	if id <= 0 {
		return PlaybackItem{}, false
	}
	for _, item := range queue {
		if item.ID == id {
			return item, true
		}
	}
	return PlaybackItem{}, false
}

func queueOrderChanged(before, after []PlaybackItem) bool {
	if len(before) != len(after) {
		return true
	}
	for i := range before {
		if before[i].ID != after[i].ID {
			return true
		}
	}
	return false
}

func (s *Server) recordRoomAction(r *http.Request, room *Room, username, text string) PlaybackState {
	ip := ""
	if parsedIP, ok := clientIP(r.RemoteAddr); ok {
		ip = parsedIP.String()
	}
	return room.Playback.AddAction(RoomAction{
		IP:       ip,
		Username: username,
		Text:     text,
	})
}

func (s *Server) skipActionText(ctx context.Context, previousKey, _ string) string {
	previousName := s.trackActionName(ctx, previousKey)
	return fmt.Sprintf("Skipped %q.", previousName)
}

func (s *Server) trackActionName(ctx context.Context, dedupeKey string) string {
	if dedupeKey == "" || s.Library == nil {
		return "Unavailable track"
	}
	track, err := s.Library.ResolveDedupeKey(ctx, dedupeKey)
	if err != nil {
		return "Unavailable track"
	}
	return trackActionTitle(track)
}

func trackActionTitle(track musiclib.Track) string {
	title := strings.TrimSpace(track.Title)
	if title == "" {
		return "Track"
	}
	return title
}

func permissionForAction(action string) (RoomPermission, bool) {
	switch action {
	case "queue_add":
		return PermissionQueueAdd, true
	case "queue_remove", "queue_reorder", "queue_clear", "history_clear", "auto_dj", "auto_dj_source":
		return PermissionQueueManage, true
	case "play", "play_now", "pause", "previous", "seek", "skip":
		return PermissionPlaybackControl, true
	case "room_audio":
		return PermissionVolumeControl, true
	default:
		return "", false
	}
}

func (s *Server) prepareAutoDJ(ctx context.Context, room *Room) error {
	config, candidate := room.Playback.AutoDJConfiguration()
	if !config.Enabled {
		return nil
	}
	if candidate != "" {
		if _, err := s.Library.ResolveDedupeKey(ctx, candidate); err == nil {
			return nil
		} else if !errors.Is(err, musiclib.ErrTrackNotFound) {
			return err
		}
		if !room.Playback.ClearAutoDJCandidate(config.Source) {
			return nil
		}
	}
	_, err := s.prepareAutoDJCandidate(ctx, room, config.Source)
	if errors.Is(err, musiclib.ErrTrackNotFound) {
		room.Playback.ConfigureAutoDJ(false, "", nil)
		return nil
	}
	if errors.Is(err, errAutoDJConfigurationChanged) {
		return nil
	}
	if err != nil {
		return err
	}
	return nil
}

func (s *Server) replenishAutoDJ(ctx context.Context, room *Room) {
	config, candidate := room.Playback.AutoDJConfiguration()
	if !config.Enabled || candidate != "" {
		return
	}
	_, err := s.prepareAutoDJCandidate(ctx, room, config.Source)
	if err != nil {
		if errors.Is(err, errAutoDJConfigurationChanged) {
			return
		}
		slog.Warn("prepare next auto-dj track", "room", room.ID, "error", err)
		if errors.Is(err, musiclib.ErrTrackNotFound) || errors.Is(err, musiclib.ErrPlaylistNotFound) {
			room.Playback.ConfigureAutoDJ(false, "", nil)
		}
		return
	}
}

func (s *Server) newAutoDJCycle(ctx context.Context, source AutoDJSource) (string, []int64, error) {
	entries, err := s.autoDJEntries(ctx, source)
	if err != nil {
		return "", nil, err
	}
	if len(entries) == 0 {
		return "", nil, musiclib.ErrTrackNotFound
	}
	rand.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
	return s.resolveAutoDJEntries(ctx, source, entries)
}

func (s *Server) autoDJEntries(ctx context.Context, source AutoDJSource) ([]int64, error) {
	if source.Type == AutoDJSourcePlaylist {
		return s.Library.PlaylistShuffleItemIDs(ctx, source.PlaylistID)
	}
	return s.Library.ShuffleTrackIDs(ctx)
}

func (s *Server) nextAutoDJCandidate(ctx context.Context, room *Room, source AutoDJSource) (string, error) {
	for {
		entry, ok := room.Playback.TakeAutoDJEntry(source)
		if ok {
			track, err := s.resolveAutoDJEntry(ctx, source, entry)
			if errors.Is(err, musiclib.ErrTrackNotFound) {
				continue
			}
			if err != nil {
				return "", err
			}
			return track.DedupeKey, nil
		}
		entries, err := s.autoDJEntries(ctx, source)
		if err != nil {
			return "", err
		}
		if len(entries) == 0 {
			return "", musiclib.ErrTrackNotFound
		}
		rand.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
		if room.Playback.RefillAutoDJEntries(source, entries) {
			continue
		}
		config, _ := room.Playback.AutoDJConfiguration()
		if !config.Enabled || config.Source != source {
			return "", errAutoDJConfigurationChanged
		}
	}
}

func (s *Server) prepareAutoDJCandidate(ctx context.Context, room *Room, source AutoDJSource) (string, error) {
	if !room.Playback.BeginAutoDJCandidate(source) {
		return "", errAutoDJConfigurationChanged
	}
	candidate, err := s.nextAutoDJCandidate(ctx, room, source)
	if err != nil {
		room.Playback.CompleteAutoDJCandidate(source, "")
		return "", err
	}
	if !room.Playback.CompleteAutoDJCandidate(source, candidate) {
		return "", errAutoDJConfigurationChanged
	}
	return candidate, nil
}

func (s *Server) resolveAutoDJEntries(ctx context.Context, source AutoDJSource, entries []int64) (string, []int64, error) {
	for len(entries) > 0 {
		last := len(entries) - 1
		entry := entries[last]
		entries = entries[:last]
		track, err := s.resolveAutoDJEntry(ctx, source, entry)
		if errors.Is(err, musiclib.ErrTrackNotFound) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		return track.DedupeKey, entries, nil
	}
	return "", nil, musiclib.ErrTrackNotFound
}

func (s *Server) resolveAutoDJEntry(ctx context.Context, source AutoDJSource, entry int64) (musiclib.Track, error) {
	if source.Type == AutoDJSourcePlaylist {
		return s.Library.PlaylistItemTrack(ctx, source.PlaylistID, entry)
	}
	return s.Library.GetCached(ctx, entry)
}

func (s *Server) resolveAutoDJSource(ctx context.Context, source AutoDJSource) (AutoDJSource, error) {
	switch source.Type {
	case AutoDJSourceLibrary:
		return defaultAutoDJSource(), nil
	case AutoDJSourcePlaylist:
		if source.PlaylistID <= 0 {
			return AutoDJSource{}, errors.New("playlist_id is required for playlist shuffle")
		}
		playlist, err := s.Library.GetPlaylistMetadata(ctx, source.PlaylistID)
		if err != nil {
			return AutoDJSource{}, err
		}
		return AutoDJSource{Type: AutoDJSourcePlaylist, PlaylistID: playlist.ID, Name: playlist.Name}, nil
	default:
		return AutoDJSource{}, errors.New("shuffle source type must be library or playlist")
	}
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func (s *Server) handleRescan(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	slog.Info("library rescan started", "remote", r.RemoteAddr)
	if err := s.Library.Scan(r.Context()); err != nil {
		if errors.Is(err, musiclib.ErrScanInProgress) {
			slog.Info("library rescan ignored; already scanning", "remote", r.RemoteAddr)
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		slog.Warn("library rescan failed", "remote", r.RemoteAddr, "duration", time.Since(started), "error", err)
		writeError(w, err)
		return
	}
	count, err := s.Library.Count(r.Context())
	if err != nil {
		slog.Warn("count library after rescan", "remote", r.RemoteAddr, "duration", time.Since(started), "error", err)
	} else {
		slog.Info("library rescan completed", "remote", r.RemoteAddr, "duration", time.Since(started), "tracks", count)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRescanDir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MusicDir string `json:"music_dir"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	dir := strings.TrimSpace(req.MusicDir)
	if dir == "" {
		http.Error(w, "music_dir is required", http.StatusBadRequest)
		return
	}
	s.configMu.RLock()
	configured := append([]string(nil), s.Config.MusicDirs...)
	s.configMu.RUnlock()
	if !slices.Contains(configured, dir) {
		http.Error(w, "music_dir must match a configured music directory", http.StatusBadRequest)
		return
	}

	started := time.Now()
	slog.Info("library directory rescan started", "remote", r.RemoteAddr, "music_dir", dir)
	if err := s.Library.ScanDir(r.Context(), dir); err != nil {
		if errors.Is(err, musiclib.ErrScanInProgress) {
			slog.Info("library directory rescan ignored; already scanning", "remote", r.RemoteAddr, "music_dir", dir)
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		slog.Warn("library directory rescan failed", "remote", r.RemoteAddr, "music_dir", dir, "duration", time.Since(started), "error", err)
		writeError(w, err)
		return
	}
	count, err := s.Library.Count(r.Context())
	if err != nil {
		slog.Warn("count library after directory rescan", "remote", r.RemoteAddr, "music_dir", dir, "duration", time.Since(started), "error", err)
	} else {
		slog.Info("library directory rescan completed", "remote", r.RemoteAddr, "music_dir", dir, "duration", time.Since(started), "tracks", count)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid media id", http.StatusBadRequest)
		return
	}
	media, err := s.Library.OpenMedia(r.Context(), id)
	if err != nil {
		if errors.Is(err, musiclib.ErrTrackNotFound) {
			slog.Warn("media track not found", "remote", r.RemoteAddr, "track_id", id)
			http.NotFound(w, r)
			return
		}
		slog.Warn("load media track", "remote", r.RemoteAddr, "track_id", id, "error", err)
		writeError(w, err)
		return
	}
	defer media.Close()
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, media.Name(), media.ModTime(), media)
}

func (s *Server) handleArtwork(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid media id", http.StatusBadRequest)
		return
	}
	data, mimeType, err := s.Library.Artwork(r.Context(), id)
	if err != nil {
		if errors.Is(err, musiclib.ErrTrackNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Warn("load media artwork", "remote", r.RemoteAddr, "track_id", id, "error", err)
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Write(data)
}

func (s *Server) writeCommandState(w http.ResponseWriter, r *http.Request, event string, room *Room, username string, state PlaybackState) {
	state = s.stabilizeAndSchedulePlayback(r.Context(), room, state)
	if err := s.savePlayback(r.Context(), room); err != nil {
		slog.Error("save playback state", "remote", r.RemoteAddr, "room", room.ID, "error", err)
		http.Error(w, "save playback state", http.StatusInternalServerError)
		return
	}
	view, err := s.viewStateForRequest(r, state)
	if err != nil {
		slog.Warn("build view state", "remote", r.RemoteAddr, "error", err)
		writeError(w, err)
		return
	}
	slog.Info("playback action",
		"action", event,
		"username", username,
		"remote", r.RemoteAddr,
		"room", room.ID,
	)
	writeJSON(w, view)
}

type ViewState struct {
	PlaybackState
	Current     *ViewItem        `json:"current"`
	Queue       []ViewItem       `json:"queue"`
	History     []ViewItem       `json:"history"`
	Permissions []RoomPermission `json:"permissions"`
}

type ViewItem struct {
	PlaybackItem
	Track *musiclib.Track `json:"track"`
}

func (s *Server) viewState(ctx context.Context, state PlaybackState) (ViewState, error) {
	keys := make([]string, 0, len(state.Queue)+len(state.History)+1)
	if state.Current.DedupeKey != "" {
		keys = append(keys, state.Current.DedupeKey)
	}
	for _, item := range state.Queue {
		keys = append(keys, item.DedupeKey)
	}
	for _, item := range state.History {
		keys = append(keys, item.DedupeKey)
	}
	tracks, err := s.Library.ListByDedupeKeys(ctx, keys)
	if err != nil {
		return ViewState{}, err
	}
	view := ViewState{PlaybackState: state}
	view.Queue = make([]ViewItem, 0, len(state.Queue))
	view.History = make([]ViewItem, 0, len(state.History))
	if state.Current.DedupeKey != "" {
		view.Current = &ViewItem{PlaybackItem: state.Current}
		if track, ok := tracks[state.Current.DedupeKey]; ok {
			view.Current.Track = &track
		}
	}
	for _, item := range state.Queue {
		viewItem := ViewItem{PlaybackItem: item}
		if track, ok := tracks[item.DedupeKey]; ok {
			viewItem.Track = &track
		}
		view.Queue = append(view.Queue, viewItem)
	}
	for _, item := range state.History {
		viewItem := ViewItem{PlaybackItem: item}
		if track, ok := tracks[item.DedupeKey]; ok {
			viewItem.Track = &track
		}
		view.History = append(view.History, viewItem)
	}
	return view, nil
}

func (s *Server) viewStateForRequest(r *http.Request, state PlaybackState) (ViewState, error) {
	user, ok := s.Auth.CurrentUser(r)
	if !ok {
		return ViewState{}, errors.New("authentication required")
	}
	permissions, ok := s.Rooms.PermissionsForUser(state.RoomID, user)
	if !ok {
		return ViewState{}, errors.New("room not found")
	}
	view, err := s.viewState(r.Context(), state)
	if err != nil {
		return ViewState{}, err
	}
	view.Permissions = permissions
	return view, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, err error) {
	if errors.Is(err, musiclib.ErrTrackNotFound) || errors.Is(err, musiclib.ErrPlaylistNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
