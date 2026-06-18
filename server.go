package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	Auth       AuthGate
	AuthRoutes http.Handler
	Library    *musiclib.Library
	Rooms      *RoomManager
	Config     Config
	ConfigPath string
	configMu   sync.RWMutex
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	requireAdmin := s.Auth.Require(RoleAdmin)
	mux.Handle("GET /admin", requireAdmin(http.HandlerFunc(s.handleAdminPage)))
	mux.Handle("GET /admin.js", requireAdmin(http.HandlerFunc(s.handleAdminJS)))
	requireListener := s.Auth.Require(RoleListener, RoleAdmin)
	webFiles := http.FileServer(http.FS(webRoot()))
	mux.Handle("GET /{$}", requireListener(http.HandlerFunc(s.handleApp)))
	mux.Handle("GET /rooms/{room}", requireListener(http.HandlerFunc(s.handleApp)))
	mux.Handle("GET /assets/", requireListener(http.StripPrefix("/assets/", webFiles)))
	mux.Handle("GET /rooms/{room}/events", requireListener(http.HandlerFunc(s.handleEvents)))
	mux.Handle("GET /api/session", requireListener(http.HandlerFunc(s.handleSession)))
	mux.Handle("GET /rooms/{room}/api/state", requireListener(http.HandlerFunc(s.handleState)))
	mux.Handle("GET /api/search", requireListener(http.HandlerFunc(s.handleSearch)))
	mux.Handle("GET /api/library", requireListener(http.HandlerFunc(s.handleLibrary)))
	mux.Handle("POST /rooms/{room}/api/command", requireListener(http.HandlerFunc(s.handleCommand)))
	mux.Handle("POST /api/admin/rescan", requireAdmin(http.HandlerFunc(s.handleRescan)))
	mux.Handle("POST /api/admin/rescan-dir", requireAdmin(http.HandlerFunc(s.handleRescanDir)))
	mux.Handle("GET /api/admin/config", requireAdmin(http.HandlerFunc(s.handleConfig)))
	mux.Handle("PUT /api/admin/config", requireAdmin(http.HandlerFunc(s.handleConfigUpdate)))
	mux.Handle("GET /media/{id}/artwork", requireListener(http.HandlerFunc(s.handleArtwork)))
	mux.Handle("GET /media/{id}", requireListener(http.HandlerFunc(s.handleMedia)))
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
	visible := make([]Room, 0, len(rooms))
	for _, room := range rooms {
		if UserCanAccessRoom(user, room) {
			visible = append(visible, room)
		}
	}
	writeJSON(w, map[string]any{
		"default_room_id": s.Rooms.DefaultID(),
		"rooms":           visible,
		"user":            user,
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
	if !UserCanAccessRoom(user, *room) {
		http.Error(w, "room access denied", http.StatusForbidden)
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
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, cancel := room.Playback.Subscribe(user)
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
			if !s.writeEvent(w, r, s.roomSnapshot(r.Context(), room)) {
				slog.Info("listener heartbeat write closed", "remote", r.RemoteAddr, "username", user.Username, "room", room.ID)
				return
			}
		}
	}
}

func (s *Server) writeEvent(w http.ResponseWriter, r *http.Request, state PlaybackState) bool {
	payload, err := s.viewState(r.Context(), state)
	if err != nil {
		slog.Warn("build sse state", "error", err)
		return true
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
	room, _, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	state, err := s.viewState(r.Context(), s.roomSnapshot(r.Context(), room))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, state)
}

func (s *Server) roomSnapshot(ctx context.Context, room *Room) PlaybackState {
	state := room.Playback.Snapshot()
	if !s.playbackExpired(ctx, state) {
		return state
	}
	slog.Info("auto advancing expired playback", "room", room.ID, "track_id", state.Current.TrackID)
	return room.Playback.Ended(state.Current.TrackID)
}

func (s *Server) playbackExpired(ctx context.Context, state PlaybackState) bool {
	if s.Library == nil || state.Current.TrackID == 0 || state.Paused || state.StartedAt.IsZero() {
		return false
	}
	track, err := s.Library.Get(ctx, state.Current.TrackID)
	if err != nil || track.DurationMS <= 0 {
		return false
	}
	return time.Since(state.StartedAt).Milliseconds() > track.DurationMS+1500
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
	s.configMu.RLock()
	old := s.Config
	path := s.ConfigPath
	s.configMu.RUnlock()

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

func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	room, user, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	var req struct {
		Action     string `json:"action"`
		TrackID    int64  `json:"track_id"`
		ID         int64  `json:"id"`
		Direction  int    `json:"direction"`
		PositionMS int64  `json:"position_ms"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	switch req.Action {
	case "queue_add":
		if req.TrackID <= 0 {
			http.Error(w, "track_id is required", http.StatusBadRequest)
			return
		}
		s.writeCommandState(w, r, "queue_add", room, user.Username, room.Playback.Add(req.TrackID, user.Username))
	case "queue_remove":
		if !requireID(w, req.ID) {
			return
		}
		s.writeCommandState(w, r, "queue_remove", room, user.Username, room.Playback.Remove(req.ID))
	case "queue_move":
		if !requireID(w, req.ID) {
			return
		}
		if req.Direction == 0 {
			http.Error(w, "direction is required", http.StatusBadRequest)
			return
		}
		event := "queue_move_down"
		delta := 1
		if req.Direction < 0 {
			event = "queue_move_up"
			delta = -1
		}
		s.writeCommandState(w, r, event, room, user.Username, room.Playback.Move(req.ID, delta))
	case "queue_next":
		if !requireID(w, req.ID) {
			return
		}
		s.writeCommandState(w, r, "queue_next", room, user.Username, room.Playback.MoveToNext(req.ID))
	case "queue_clear":
		s.writeCommandState(w, r, "queue_clear", room, user.Username, room.Playback.Clear())
	case "play":
		state, err := room.Playback.Play()
		if err != nil {
			slog.Warn("play rejected", "remote", r.RemoteAddr, "room", room.ID, "error", err)
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		s.writeCommandState(w, r, "play", room, user.Username, state)
	case "play_now":
		if req.TrackID <= 0 {
			http.Error(w, "track_id is required", http.StatusBadRequest)
			return
		}
		s.writeCommandState(w, r, "play_now", room, user.Username, room.Playback.PlayNow(req.TrackID, user.Username))
	case "pause":
		s.writeCommandState(w, r, "pause", room, user.Username, room.Playback.Pause())
	case "previous":
		s.writeCommandState(w, r, "previous", room, user.Username, room.Playback.Previous())
	case "seek":
		s.writeCommandState(w, r, "seek", room, user.Username, room.Playback.Seek(req.PositionMS))
	case "skip":
		s.writeCommandState(w, r, "skip", room, user.Username, room.Playback.Skip())
	case "history_clear":
		s.writeCommandState(w, r, "history_clear", room, user.Username, room.Playback.ClearHistory())
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
	}
}

func requireID(w http.ResponseWriter, id int64) bool {
	if id <= 0 {
		http.Error(w, "id is required", http.StatusBadRequest)
		return false
	}
	return true
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
	view, err := s.viewState(r.Context(), state)
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
	Current *ViewItem  `json:"current"`
	Queue   []ViewItem `json:"queue"`
	History []ViewItem `json:"history"`
}

type ViewItem struct {
	PlaybackItem
	Track *musiclib.Track `json:"track"`
}

func (s *Server) viewState(ctx context.Context, state PlaybackState) (ViewState, error) {
	ids := make([]int64, 0, len(state.Queue)+len(state.History)+1)
	if state.Current.TrackID != 0 {
		ids = append(ids, state.Current.TrackID)
	}
	for _, item := range state.Queue {
		ids = append(ids, item.TrackID)
	}
	for _, item := range state.History {
		ids = append(ids, item.TrackID)
	}
	tracks, err := s.Library.ListByIDs(ctx, ids)
	if err != nil {
		return ViewState{}, err
	}
	view := ViewState{PlaybackState: state}
	view.Queue = make([]ViewItem, 0, len(state.Queue))
	view.History = make([]ViewItem, 0, len(state.History))
	if state.Current.TrackID != 0 {
		view.Current = &ViewItem{PlaybackItem: state.Current}
		if track, ok := tracks[state.Current.TrackID]; ok {
			view.Current.Track = &track
		}
	}
	for _, item := range state.Queue {
		viewItem := ViewItem{PlaybackItem: item}
		if track, ok := tracks[item.TrackID]; ok {
			viewItem.Track = &track
		}
		view.Queue = append(view.Queue, viewItem)
	}
	for _, item := range state.History {
		viewItem := ViewItem{PlaybackItem: item}
		if track, ok := tracks[item.TrackID]; ok {
			viewItem.Track = &track
		}
		view.History = append(view.History, viewItem)
	}
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
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
