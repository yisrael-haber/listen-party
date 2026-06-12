package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	musiclib "listen-party/internal/library"
)

type ServerOptions struct {
	Auth       *BasicAuth
	Library    *musiclib.Library
	Player     *Playback
	Config     Config
	ConfigPath string
	RoomID     string
	Logger     *slog.Logger
}

type Server struct {
	auth       *BasicAuth
	library    *musiclib.Library
	player     *Playback
	configMu   sync.RWMutex
	config     Config
	configPath string
	roomID     string
	logger     *slog.Logger
}

func NewServer(opts ServerOptions) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Server{
		auth:       opts.Auth,
		library:    opts.Library,
		player:     opts.Player,
		config:     opts.Config,
		configPath: opts.ConfigPath,
		roomID:     opts.RoomID,
		logger:     opts.Logger,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	requireAdmin := s.auth.RequireRealm("listen-party-admin", RoleAdmin)
	mux.Handle("GET /admin", requireAdmin(http.HandlerFunc(s.handleAdminPage)))
	mux.Handle("GET /admin/", requireAdmin(http.HandlerFunc(s.handleAdminPage)))
	mux.Handle("GET /admin.js", requireAdmin(http.HandlerFunc(s.handleAdminJS)))
	mux.Handle("GET /", s.auth.Require(RoleListener, RoleAdmin)(http.FileServer(http.FS(webRoot()))))
	mux.Handle("GET /events", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handleEvents)))
	mux.Handle("GET /api/state", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handleState)))
	mux.Handle("GET /api/search", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handleSearch)))
	mux.Handle("GET /api/library", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handleLibrary)))
	mux.Handle("POST /api/queue", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handleQueue)))
	mux.Handle("POST /api/queue/move", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handleQueueMove)))
	mux.Handle("POST /api/queue/next", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handleQueueNext)))
	mux.Handle("POST /api/history/clear", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handleHistoryClear)))
	mux.Handle("POST /api/playback/play", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handlePlay)))
	mux.Handle("POST /api/playback/play-now", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handlePlayNow)))
	mux.Handle("POST /api/playback/pause", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handlePause)))
	mux.Handle("POST /api/playback/seek", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handleSeek)))
	mux.Handle("POST /api/playback/ended", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handleEnded)))
	mux.Handle("POST /api/playback/previous", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handlePrevious)))
	mux.Handle("POST /api/playback/skip", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handleSkip)))
	mux.Handle("POST /api/queue/remove", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handleQueueRemove)))
	mux.Handle("POST /api/queue/clear", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handleQueueClear)))
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
	return mux
}

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	http.ServeFileFS(w, r, adminRoot(), "admin.html")
}

func (s *Server) handleAdminJS(w http.ResponseWriter, r *http.Request) {
	http.ServeFileFS(w, r, adminRoot(), "admin.js")
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, cancel := s.player.Subscribe(s.roomID)
	s.logger.Info("listener connected", "remote", r.RemoteAddr, "listener_count", s.player.Snapshot(s.roomID).ListenerCount)
	defer func() {
		cancel()
		s.logger.Info("listener disconnected", "remote", r.RemoteAddr, "listener_count", s.player.Snapshot(s.roomID).ListenerCount)
	}()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case state := <-ch:
			if !s.writeEvent(w, r, state) {
				return
			}
		case <-ticker.C:
			if !s.writeEvent(w, r, s.player.Snapshot(s.roomID)) {
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
	state, err := s.viewState(r.Context(), s.player.Snapshot(s.roomID))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, state)
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

	s.auth.Update(cfg.Auth)
	s.library.UpdateScanConfig(cfg.MusicDirs, cfg.ScanWorkers)

	s.configMu.Lock()
	s.config = cfg
	s.configMu.Unlock()

	s.logger.Info("config updated",
		"remote", r.RemoteAddr,
		"path", path,
		"addr_changed", cfg.Addr != old.Addr,
		"database_changed", cfg.DatabasePath != old.DatabasePath,
		"music_dirs", len(cfg.MusicDirs),
		"scan_workers", cfg.ScanWorkers,
	)
	writeJSON(w, ConfigView{
		Path:          path,
		Config:        cfg,
		RestartNeeded: cfg.Addr != old.Addr || cfg.DatabasePath != old.DatabasePath,
	})
}

func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
	trackID, ok := s.readTrackID(w, r)
	if !ok {
		return
	}
	s.writeCommandState(w, r, "queue_add", s.player.Add(s.roomID, trackID), "track_id", trackID)
}

func (s *Server) handleQueueRemove(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r)
	if !ok {
		return
	}
	s.writeCommandState(w, r, "queue_remove", s.player.Remove(s.roomID, id), "queue_item_id", id)
}

func (s *Server) handleQueueMove(w http.ResponseWriter, r *http.Request) {
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
		s.writeCommandState(w, r, "queue_move", s.player.Move(s.roomID, req.ID, -1), "queue_item_id", req.ID, "direction", -1)
		return
	}
	if req.Direction > 0 {
		s.writeCommandState(w, r, "queue_move", s.player.Move(s.roomID, req.ID, 1), "queue_item_id", req.ID, "direction", 1)
		return
	}
	s.writeViewState(w, r, s.player.Snapshot(s.roomID))
}

func (s *Server) handleQueueNext(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r)
	if !ok {
		return
	}
	s.writeCommandState(w, r, "queue_next", s.player.MoveToNext(s.roomID, id), "queue_item_id", id)
}

func (s *Server) handleQueueClear(w http.ResponseWriter, r *http.Request) {
	s.writeCommandState(w, r, "queue_clear", s.player.Clear(s.roomID))
}

func (s *Server) handleHistoryClear(w http.ResponseWriter, r *http.Request) {
	s.writeCommandState(w, r, "history_clear", s.player.ClearHistory(s.roomID))
}

func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request) {
	state, err := s.player.Play(s.roomID)
	if err != nil {
		s.logger.Warn("play rejected", "remote", r.RemoteAddr, "error", err)
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	s.writeCommandState(w, r, "play", state)
}

func (s *Server) handlePlayNow(w http.ResponseWriter, r *http.Request) {
	trackID, ok := s.readTrackID(w, r)
	if !ok {
		return
	}
	s.writeCommandState(w, r, "play_now", s.player.PlayNow(s.roomID, trackID), "track_id", trackID)
}

func (s *Server) readTrackID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	var req struct {
		TrackID int64 `json:"track_id"`
	}
	if !readJSON(w, r, &req) {
		return 0, false
	}
	if req.TrackID <= 0 {
		http.Error(w, "track_id is required", http.StatusBadRequest)
		return 0, false
	}
	if _, err := s.library.Get(r.Context(), req.TrackID); err != nil {
		if errors.Is(err, musiclib.ErrTrackNotFound) {
			s.logger.Warn("track not found", "remote", r.RemoteAddr, "track_id", req.TrackID, "path", r.URL.Path)
			http.Error(w, "track not found", http.StatusNotFound)
			return 0, false
		}
		s.logger.Warn("validate track", "remote", r.RemoteAddr, "track_id", req.TrackID, "path", r.URL.Path, "error", err)
		writeError(w, err)
		return 0, false
	}
	return req.TrackID, true
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	s.writeCommandState(w, r, "pause", s.player.Pause(s.roomID))
}

func (s *Server) handlePrevious(w http.ResponseWriter, r *http.Request) {
	s.writeCommandState(w, r, "previous", s.player.Previous(s.roomID))
}

func (s *Server) handleSeek(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PositionMS int64 `json:"position_ms"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	s.writeCommandState(w, r, "seek", s.player.Seek(s.roomID, req.PositionMS), "position_ms", req.PositionMS)
}

func (s *Server) handleSkip(w http.ResponseWriter, r *http.Request) {
	s.writeCommandState(w, r, "skip", s.player.Skip(s.roomID))
}

func (s *Server) handleEnded(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TrackID int64 `json:"track_id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.TrackID <= 0 {
		http.Error(w, "track_id is required", http.StatusBadRequest)
		return
	}
	s.writeCommandState(w, r, "track_ended", s.player.Ended(s.roomID, req.TrackID), "track_id", req.TrackID)
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
	s.logPlayback(event, r, state, args...)
	s.writeViewState(w, r, state)
}

func (s *Server) logPlayback(event string, r *http.Request, state PlaybackState, args ...any) {
	fields := append([]any{
		"remote", r.RemoteAddr,
		"revision", state.Revision,
		"playback_id", state.PlaybackID,
		"current_track_id", state.CurrentTrackID,
		"queue_len", len(state.Queue),
		"history_len", len(state.History),
		"paused", state.Paused,
	}, args...)
	s.logger.Info(event, fields...)
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
