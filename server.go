package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	musiclib "listen-party/internal/library"
)

type ServerOptions struct {
	Auth       AuthGate
	AuthRoutes http.Handler
	Library    *musiclib.Library
	Rooms      *RoomManager
	Config     Config
	ConfigPath string
	Logger     *slog.Logger
}

type Server struct {
	auth       AuthGate
	authRoutes http.Handler
	library    *musiclib.Library
	rooms      *RoomManager
	configMu   sync.RWMutex
	config     Config
	configPath string
	logger     *slog.Logger
}

func NewServer(opts ServerOptions) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Rooms == nil {
		if len(opts.Config.Rooms) > 0 {
			opts.Rooms = NewRoomManager(opts.Config.Rooms)
		} else {
			opts.Rooms = NewRoomManager([]RoomConfig{{ID: defaultRoomID, Name: "Public Room", Public: true}})
		}
	}
	return &Server{
		auth:       opts.Auth,
		authRoutes: opts.AuthRoutes,
		library:    opts.Library,
		rooms:      opts.Rooms,
		config:     opts.Config,
		configPath: opts.ConfigPath,
		logger:     opts.Logger,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	requireAdmin := s.auth.Require(RoleAdmin)
	mux.Handle("GET /admin", requireAdmin(http.HandlerFunc(s.handleAdminPage)))
	mux.Handle("GET /admin/", requireAdmin(http.HandlerFunc(s.handleAdminPage)))
	mux.Handle("GET /admin.js", requireAdmin(http.HandlerFunc(s.handleAdminJS)))
	requireListener := s.auth.Require(RoleListener, RoleAdmin)
	webFiles := http.FileServer(http.FS(webRoot()))
	mux.Handle("GET /{$}", requireListener(http.HandlerFunc(s.handleDefaultRoom)))
	mux.Handle("GET /rooms/{room}", requireListener(http.HandlerFunc(s.handleRoomPage)))
	mux.Handle("GET /rooms/{room}/", requireListener(http.HandlerFunc(s.handleRoomPage)))
	mux.Handle("GET /assets/", requireListener(http.StripPrefix("/assets/", webFiles)))
	mux.Handle("GET /events", requireListener(http.HandlerFunc(s.handleEvents)))
	mux.Handle("GET /rooms/{room}/events", requireListener(http.HandlerFunc(s.handleEvents)))
	mux.Handle("GET /api/me", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handleMe)))
	mux.Handle("GET /api/rooms", requireListener(http.HandlerFunc(s.handleRooms)))
	mux.Handle("GET /api/state", requireListener(http.HandlerFunc(s.handleState)))
	mux.Handle("GET /rooms/{room}/api/state", requireListener(http.HandlerFunc(s.handleState)))
	mux.Handle("GET /api/search", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handleSearch)))
	mux.Handle("GET /api/library", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handleLibrary)))
	mux.Handle("POST /api/queue", requireListener(http.HandlerFunc(s.handleQueue)))
	mux.Handle("POST /rooms/{room}/api/queue", requireListener(http.HandlerFunc(s.handleQueue)))
	mux.Handle("POST /api/queue/move", requireListener(http.HandlerFunc(s.handleQueueMove)))
	mux.Handle("POST /rooms/{room}/api/queue/move", requireListener(http.HandlerFunc(s.handleQueueMove)))
	mux.Handle("POST /api/queue/next", requireListener(http.HandlerFunc(s.handleQueueNext)))
	mux.Handle("POST /rooms/{room}/api/queue/next", requireListener(http.HandlerFunc(s.handleQueueNext)))
	mux.Handle("POST /api/history/clear", requireListener(http.HandlerFunc(s.handleHistoryClear)))
	mux.Handle("POST /rooms/{room}/api/history/clear", requireListener(http.HandlerFunc(s.handleHistoryClear)))
	mux.Handle("POST /api/playback/play", requireListener(http.HandlerFunc(s.handlePlay)))
	mux.Handle("POST /rooms/{room}/api/playback/play", requireListener(http.HandlerFunc(s.handlePlay)))
	mux.Handle("POST /api/playback/play-now", requireListener(http.HandlerFunc(s.handlePlayNow)))
	mux.Handle("POST /rooms/{room}/api/playback/play-now", requireListener(http.HandlerFunc(s.handlePlayNow)))
	mux.Handle("POST /api/playback/pause", requireListener(http.HandlerFunc(s.handlePause)))
	mux.Handle("POST /rooms/{room}/api/playback/pause", requireListener(http.HandlerFunc(s.handlePause)))
	mux.Handle("POST /api/playback/seek", requireListener(http.HandlerFunc(s.handleSeek)))
	mux.Handle("POST /rooms/{room}/api/playback/seek", requireListener(http.HandlerFunc(s.handleSeek)))
	mux.Handle("POST /api/playback/ended", requireListener(http.HandlerFunc(s.handleEnded)))
	mux.Handle("POST /rooms/{room}/api/playback/ended", requireListener(http.HandlerFunc(s.handleEnded)))
	mux.Handle("POST /api/playback/previous", requireListener(http.HandlerFunc(s.handlePrevious)))
	mux.Handle("POST /rooms/{room}/api/playback/previous", requireListener(http.HandlerFunc(s.handlePrevious)))
	mux.Handle("POST /api/playback/skip", requireListener(http.HandlerFunc(s.handleSkip)))
	mux.Handle("POST /rooms/{room}/api/playback/skip", requireListener(http.HandlerFunc(s.handleSkip)))
	mux.Handle("POST /api/queue/remove", requireListener(http.HandlerFunc(s.handleQueueRemove)))
	mux.Handle("POST /rooms/{room}/api/queue/remove", requireListener(http.HandlerFunc(s.handleQueueRemove)))
	mux.Handle("POST /api/queue/clear", requireListener(http.HandlerFunc(s.handleQueueClear)))
	mux.Handle("POST /rooms/{room}/api/queue/clear", requireListener(http.HandlerFunc(s.handleQueueClear)))
	mux.Handle("POST /api/admin/play", requireAdmin(http.HandlerFunc(s.handlePlay)))
	mux.Handle("POST /api/admin/pause", requireAdmin(http.HandlerFunc(s.handlePause)))
	mux.Handle("POST /api/admin/skip", requireAdmin(http.HandlerFunc(s.handleSkip)))
	mux.Handle("POST /api/admin/rescan", requireAdmin(http.HandlerFunc(s.handleRescan)))
	mux.Handle("GET /api/admin/config", requireAdmin(http.HandlerFunc(s.handleConfig)))
	mux.Handle("PUT /api/admin/config", requireAdmin(http.HandlerFunc(s.handleConfigUpdate)))
	mux.Handle("GET /media/", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handleMedia)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	if s.authRoutes == nil {
		return mux
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAuthRoute(r.URL.Path) {
			s.authRoutes.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func isAuthRoute(path string) bool {
	if path == "/login" || strings.HasPrefix(path, "/login/") ||
		path == "/logout" || strings.HasPrefix(path, "/logout/") ||
		path == "/authAdmin" || strings.HasPrefix(path, "/authAdmin/") ||
		strings.HasPrefix(path, "/_/") {
		return true
	}
	for _, prefix := range []string{
		"/api/backups",
		"/api/batch",
		"/api/collections",
		"/api/crons",
		"/api/files",
		"/api/health",
		"/api/logs",
		"/api/realtime",
		"/api/settings",
	} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func (s *Server) handleDefaultRoom(w http.ResponseWriter, r *http.Request) {
	http.ServeFileFS(w, r, webRoot(), "index.html")
}

func (s *Server) handleRoomPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.roomFromRequest(w, r); !ok {
		return
	}
	http.ServeFileFS(w, r, webRoot(), "index.html")
}

func (s *Server) handleRooms(w http.ResponseWriter, r *http.Request) {
	user, ok := s.auth.CurrentUser(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	rooms := s.rooms.List()
	visible := make([]Room, 0, len(rooms))
	for _, room := range rooms {
		if UserCanAccessRoom(user, room) {
			visible = append(visible, room)
		}
	}
	writeJSON(w, map[string]any{
		"default_room_id": s.rooms.DefaultID(),
		"rooms":           visible,
	})
}

func (s *Server) roomFromRequest(w http.ResponseWriter, r *http.Request) (*Room, bool) {
	roomID := r.PathValue("room")
	if roomID == "" {
		roomID = s.rooms.DefaultID()
	}
	room, ok := s.rooms.Get(roomID)
	if !ok {
		http.Error(w, "room not found", http.StatusNotFound)
		return nil, false
	}
	user, ok := s.auth.CurrentUser(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return nil, false
	}
	if !UserCanAccessRoom(user, *room) {
		http.Error(w, "room access denied", http.StatusForbidden)
		return nil, false
	}
	return room, true
}

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	http.ServeFileFS(w, r, adminRoot(), "admin.html")
}

func (s *Server) handleAdminJS(w http.ResponseWriter, r *http.Request) {
	http.ServeFileFS(w, r, adminRoot(), "admin.js")
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, ok := s.auth.CurrentUser(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	writeJSON(w, user)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	room, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	user, ok := s.auth.CurrentUser(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, cancel := room.Playback.Subscribe(ActiveListener{UserID: user.ID, Username: user.Username})
	s.logger.Info("listener connected", "remote", r.RemoteAddr, "username", user.Username, "room", room.ID, "listener_count", room.Playback.Snapshot().ListenerCount)
	defer func() {
		cancel()
		s.logger.Info("listener disconnected", "remote", r.RemoteAddr, "username", user.Username, "room", room.ID, "listener_count", room.Playback.Snapshot().ListenerCount)
	}()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			s.logger.Info("listener request closed", "remote", r.RemoteAddr, "username", user.Username, "room", room.ID, "error", r.Context().Err())
			return
		case state, ok := <-ch:
			if !ok {
				s.logger.Info("listener subscription closed", "remote", r.RemoteAddr, "username", user.Username, "room", room.ID)
				return
			}
			if !s.writeEvent(w, r, state) {
				s.logger.Info("listener sse write closed", "remote", r.RemoteAddr, "username", user.Username, "room", room.ID)
				return
			}
		case <-ticker.C:
			if !s.writeEvent(w, r, s.roomSnapshot(r.Context(), room)) {
				s.logger.Info("listener heartbeat write closed", "remote", r.RemoteAddr, "username", user.Username, "room", room.ID)
				return
			}
		}
	}
}

func (s *Server) writeEvent(w http.ResponseWriter, r *http.Request, state PlaybackState) bool {
	payload, err := s.viewState(r.Context(), state)
	if err != nil {
		s.logger.Warn("build sse state", "error", err)
		return true
	}
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		s.logger.Debug("set sse write deadline", "error", err)
	}
	fmt.Fprintf(w, "event: state\ndata: ")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		s.logger.Warn("write sse state", "error", err)
		return false
	}
	fmt.Fprint(w, "\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return true
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	room, ok := s.roomFromRequest(w, r)
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
	s.logger.Info("auto advancing expired playback", "room", room.ID, "track_id", state.CurrentTrackID, "playback_id", state.PlaybackID)
	return room.Playback.Ended(state.CurrentTrackID)
}

func (s *Server) playbackExpired(ctx context.Context, state PlaybackState) bool {
	if s.library == nil || state.CurrentTrackID == 0 || state.Paused || state.StartedAt.IsZero() {
		return false
	}
	track, err := s.library.Get(ctx, state.CurrentTrackID)
	if err != nil || track.DurationMS <= 0 {
		return false
	}
	return time.Since(state.StartedAt).Milliseconds() > track.DurationMS+1500
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	tracks, err := s.library.Search(r.Context(), q)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, tracks)
}

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	count, err := s.library.Count(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, map[string]any{
		"track_count": count,
		"scan":        s.library.ScanStatus(),
	})
}

type ConfigView struct {
	Path          string `json:"path"`
	Config        Config `json:"config"`
	RestartNeeded bool   `json:"restart_needed"`
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	view := ConfigView{
		Path:   s.configPath,
		Config: s.config,
	}
	s.configMu.RUnlock()
	writeJSON(w, view)
}

func (s *Server) handleConfigUpdate(w http.ResponseWriter, r *http.Request) {
	var cfg Config
	if !readJSON(w, r, &cfg) {
		return
	}
	if err := cfg.ApplyDefaults(); err != nil {
		s.logger.Warn("reject config update", "remote", r.RemoteAddr, "error", err)
		writeError(w, err)
		return
	}

	s.configMu.RLock()
	old := s.config
	path := s.configPath
	s.configMu.RUnlock()

	if err := SaveConfig(path, cfg); err != nil {
		s.logger.Warn("save config failed", "remote", r.RemoteAddr, "path", path, "error", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.library.UpdateScanConfig(cfg.MusicDirs, cfg.ScanWorkers)
	s.rooms.Update(cfg.Rooms)

	s.configMu.Lock()
	s.config = cfg
	s.configMu.Unlock()

	s.logger.Info("config updated",
		"remote", r.RemoteAddr,
		"path", path,
		"addr_changed", cfg.Addr != old.Addr,
		"database_changed", cfg.DatabasePath != old.DatabasePath,
		"auth_changed", cfg.Auth.PocketBase != old.Auth.PocketBase,
		"music_dirs", len(cfg.MusicDirs),
		"scan_workers", cfg.ScanWorkers,
	)
	writeJSON(w, ConfigView{
		Path:          path,
		Config:        cfg,
		RestartNeeded: cfg.Addr != old.Addr || cfg.DatabasePath != old.DatabasePath || cfg.Auth.PocketBase != old.Auth.PocketBase,
	})
}

func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
	room, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	track, ok := s.readTrack(w, r)
	if !ok {
		return
	}
	s.writeCommandState(w, r, "queue_add", room.Playback.Add(track.ID, s.actorUsername(r)), append([]any{"room", room.ID}, logTrack(track)...)...)
}

func (s *Server) handleQueueRemove(w http.ResponseWriter, r *http.Request) {
	room, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	id, ok := readID(w, r)
	if !ok {
		return
	}
	logFields := append([]any{"room", room.ID}, s.logQueueItemTrack(r, room, id)...)
	s.writeCommandState(w, r, "queue_remove", room.Playback.Remove(id), logFields...)
}

func (s *Server) handleQueueMove(w http.ResponseWriter, r *http.Request) {
	room, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	var req struct {
		ID        int64 `json:"id"`
		Direction int   `json:"direction"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.ID <= 0 {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	if req.Direction < 0 {
		s.writeCommandState(w, r, "queue_move_up", room.Playback.Move(req.ID, -1), append([]any{"room", room.ID}, s.logQueueItemTrack(r, room, req.ID)...)...)
		return
	}
	if req.Direction > 0 {
		s.writeCommandState(w, r, "queue_move_down", room.Playback.Move(req.ID, 1), append([]any{"room", room.ID}, s.logQueueItemTrack(r, room, req.ID)...)...)
		return
	}
	s.writeViewState(w, r, room.Playback.Snapshot())
}

func (s *Server) handleQueueNext(w http.ResponseWriter, r *http.Request) {
	room, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	id, ok := readID(w, r)
	if !ok {
		return
	}
	s.writeCommandState(w, r, "queue_next", room.Playback.MoveToNext(id), append([]any{"room", room.ID}, s.logQueueItemTrack(r, room, id)...)...)
}

func (s *Server) handleQueueClear(w http.ResponseWriter, r *http.Request) {
	room, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	queueLen := len(room.Playback.Snapshot().Queue)
	s.writeCommandState(w, r, "queue_clear", room.Playback.Clear(), "room", room.ID, "queue_count", queueLen)
}

func (s *Server) handleHistoryClear(w http.ResponseWriter, r *http.Request) {
	room, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	historyLen := len(room.Playback.Snapshot().History)
	s.writeCommandState(w, r, "history_clear", room.Playback.ClearHistory(), "room", room.ID, "history_count", historyLen)
}

func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request) {
	room, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	state, err := room.Playback.Play()
	if err != nil {
		s.logger.Warn("play rejected", "remote", r.RemoteAddr, "room", room.ID, "error", err)
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	s.writeCommandState(w, r, "play", state, "room", room.ID)
}

func (s *Server) handlePlayNow(w http.ResponseWriter, r *http.Request) {
	room, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	track, ok := s.readTrack(w, r)
	if !ok {
		return
	}
	s.writeCommandState(w, r, "play_now", room.Playback.PlayNow(track.ID, s.actorUsername(r)), append([]any{"room", room.ID}, logTrack(track)...)...)
}

func (s *Server) actorUsername(r *http.Request) string {
	user, ok := s.auth.CurrentUser(r)
	if !ok {
		return ""
	}
	return user.Username
}

func (s *Server) readTrack(w http.ResponseWriter, r *http.Request) (musiclib.Track, bool) {
	var req struct {
		TrackID int64 `json:"track_id"`
	}
	if !readJSON(w, r, &req) {
		return musiclib.Track{}, false
	}
	if req.TrackID <= 0 {
		http.Error(w, "track_id is required", http.StatusBadRequest)
		return musiclib.Track{}, false
	}
	track, err := s.library.Get(r.Context(), req.TrackID)
	if err != nil {
		if errors.Is(err, musiclib.ErrTrackNotFound) {
			s.logger.Warn("track not found", "remote", r.RemoteAddr, "track_id", req.TrackID, "path", r.URL.Path)
			http.Error(w, "track not found", http.StatusNotFound)
			return musiclib.Track{}, false
		}
		s.logger.Warn("validate track", "remote", r.RemoteAddr, "track_id", req.TrackID, "path", r.URL.Path, "error", err)
		writeError(w, err)
		return musiclib.Track{}, false
	}
	return track, true
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	room, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	s.writeCommandState(w, r, "pause", room.Playback.Pause(), "room", room.ID)
}

func (s *Server) handlePrevious(w http.ResponseWriter, r *http.Request) {
	room, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	s.writeCommandState(w, r, "previous", room.Playback.Previous(), "room", room.ID)
}

func (s *Server) handleSeek(w http.ResponseWriter, r *http.Request) {
	room, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	var req struct {
		PositionMS int64 `json:"position_ms"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	s.writeCommandState(w, r, "seek", room.Playback.Seek(req.PositionMS), "room", room.ID)
}

func (s *Server) handleSkip(w http.ResponseWriter, r *http.Request) {
	room, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	s.writeCommandState(w, r, "skip", room.Playback.Skip(), "room", room.ID)
}

func (s *Server) handleEnded(w http.ResponseWriter, r *http.Request) {
	room, ok := s.roomFromRequest(w, r)
	if !ok {
		return
	}
	track, ok := s.readTrack(w, r)
	if !ok {
		return
	}
	s.writeCommandState(w, r, "track_ended", room.Playback.Ended(track.ID), append([]any{"room", room.ID}, logTrack(track)...)...)
}

func (s *Server) handleRescan(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	s.logger.Info("library rescan started", "remote", r.RemoteAddr)
	if err := s.library.Scan(r.Context()); err != nil {
		if errors.Is(err, musiclib.ErrScanInProgress) {
			s.logger.Info("library rescan ignored; already scanning", "remote", r.RemoteAddr)
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		s.logger.Warn("library rescan failed", "remote", r.RemoteAddr, "duration", time.Since(started), "error", err)
		writeError(w, err)
		return
	}
	count, err := s.library.Count(r.Context())
	if err != nil {
		s.logger.Warn("count library after rescan", "remote", r.RemoteAddr, "duration", time.Since(started), "error", err)
	} else {
		s.logger.Info("library rescan completed", "remote", r.RemoteAddr, "duration", time.Since(started), "tracks", count)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimPrefix(r.URL.Path, "/media/")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid media id", http.StatusBadRequest)
		return
	}
	media, err := s.library.OpenMedia(r.Context(), id)
	if err != nil {
		if errors.Is(err, musiclib.ErrTrackNotFound) {
			s.logger.Warn("media track not found", "remote", r.RemoteAddr, "track_id", id)
			http.NotFound(w, r)
			return
		}
		s.logger.Warn("load media track", "remote", r.RemoteAddr, "track_id", id, "error", err)
		writeError(w, err)
		return
	}
	defer media.Close()
	w.Header().Set("Content-Type", "audio/mpeg")
	http.ServeContent(w, r, media.Name(), media.ModTime(), media)
}

func (s *Server) writeViewState(w http.ResponseWriter, r *http.Request, state PlaybackState) {
	view, err := s.viewState(r.Context(), state)
	if err != nil {
		s.logger.Warn("build view state", "remote", r.RemoteAddr, "revision", state.Revision, "error", err)
		writeError(w, err)
		return
	}
	writeJSON(w, view)
}

func (s *Server) writeCommandState(w http.ResponseWriter, r *http.Request, event string, state PlaybackState, args ...any) {
	view, err := s.viewState(r.Context(), state)
	if err != nil {
		s.logger.Warn("build view state", "remote", r.RemoteAddr, "revision", state.Revision, "error", err)
		writeError(w, err)
		return
	}
	s.logPlayback(event, r, view, args...)
	writeJSON(w, view)
}

func (s *Server) logPlayback(event string, r *http.Request, view ViewState, args ...any) {
	fields := append([]any{
		"action", event,
		"username", s.actorUsername(r),
		"ip", remoteIP(r.RemoteAddr),
	}, args...)
	if !hasLogKey(fields, "song_title") && logCurrentSong(event) && view.Current != nil {
		fields = append(fields, "song_title", view.Current.Title)
	}
	s.logger.Info("playback action", fields...)
}

func (s *Server) logQueueItemTrack(r *http.Request, room *Room, queueItemID int64) []any {
	for _, item := range room.Playback.Snapshot().Queue {
		if item.ID != queueItemID {
			continue
		}
		track, err := s.library.Get(r.Context(), item.TrackID)
		if err != nil {
			s.logger.Warn("lookup queue item for log", "ip", remoteIP(r.RemoteAddr), "queue_item_id", queueItemID, "track_id", item.TrackID, "error", err)
			return nil
		}
		return logTrack(track)
	}
	return nil
}

func logTrack(track musiclib.Track) []any {
	if track.Title == "" {
		return nil
	}
	return []any{"song_title", track.Title}
}

func logCurrentSong(event string) bool {
	switch event {
	case "play", "pause", "previous", "seek", "skip":
		return true
	default:
		return false
	}
}

func hasLogKey(fields []any, key string) bool {
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i] == key {
			return true
		}
	}
	return false
}

func remoteIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

type ViewState struct {
	PlaybackState
	Current *musiclib.Track   `json:"current"`
	Queue   []ViewQueueItem   `json:"queue"`
	History []ViewHistoryItem `json:"history"`
}

type ViewQueueItem struct {
	QueueItem
	Track *musiclib.Track `json:"track"`
}

type ViewHistoryItem struct {
	PlayedItem
	Track *musiclib.Track `json:"track"`
}

func (s *Server) viewState(ctx context.Context, state PlaybackState) (ViewState, error) {
	ids := make([]int64, 0, len(state.Queue)+len(state.History)+1)
	if state.CurrentTrackID != 0 {
		ids = append(ids, state.CurrentTrackID)
	}
	for _, item := range state.Queue {
		ids = append(ids, item.TrackID)
	}
	for _, item := range state.History {
		ids = append(ids, item.TrackID)
	}
	tracks, err := s.library.ListByIDs(ctx, ids)
	if err != nil {
		return ViewState{}, err
	}
	view := ViewState{PlaybackState: state}
	view.Queue = make([]ViewQueueItem, 0, len(state.Queue))
	view.History = make([]ViewHistoryItem, 0, len(state.History))
	if track, ok := tracks[state.CurrentTrackID]; ok {
		view.Current = &track
	}
	for _, item := range state.Queue {
		viewItem := ViewQueueItem{QueueItem: item}
		if track, ok := tracks[item.TrackID]; ok {
			viewItem.Track = &track
		}
		view.Queue = append(view.Queue, viewItem)
	}
	for _, item := range state.History {
		viewItem := ViewHistoryItem{PlayedItem: item}
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

func readID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	var req struct {
		ID int64 `json:"id"`
	}
	if !readJSON(w, r, &req) {
		return 0, false
	}
	if req.ID <= 0 {
		http.Error(w, "id is required", http.StatusBadRequest)
		return 0, false
	}
	return req.ID, true
}

func writeError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
