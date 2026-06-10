package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ServerOptions struct {
	Auth       *BasicAuth
	Library    *Store
	Player     *Playback
	Scanner    *Scanner
	Config     Config
	ConfigPath string
	RoomID     string
	Logger     *slog.Logger
}

type Server struct {
	auth       *BasicAuth
	library    *Store
	player     *Playback
	scanner    *Scanner
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
		scanner:    opts.Scanner,
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
	mux.Handle("POST /api/playback/play", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handlePlay)))
	mux.Handle("POST /api/playback/play-now", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handlePlayNow)))
	mux.Handle("POST /api/playback/pause", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handlePause)))
	mux.Handle("POST /api/playback/seek", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handleSeek)))
	mux.Handle("POST /api/playback/ended", s.auth.Require(RoleListener, RoleAdmin)(http.HandlerFunc(s.handleEnded)))
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
	defer cancel()
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
	tracks, err := s.library.Search(r.Context(), q, 25)
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
	writeJSON(w, map[string]int64{"track_count": count})
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
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := cfg.ApplyDefaults(); err != nil {
		writeError(w, err)
		return
	}

	s.configMu.RLock()
	old := s.config
	path := s.configPath
	s.configMu.RUnlock()

	if err := SaveConfig(path, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.auth.Update(cfg.Auth)
	s.scanner.UpdateDirs(cfg.MusicDirs)

	s.configMu.Lock()
	s.config = cfg
	s.configMu.Unlock()

	writeJSON(w, ConfigView{
		Path:          path,
		Config:        cfg,
		RestartNeeded: cfg.Addr != old.Addr || cfg.DatabasePath != old.DatabasePath,
	})
}

func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TrackID int64 `json:"track_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.TrackID <= 0 {
		http.Error(w, "track_id is required", http.StatusBadRequest)
		return
	}
	if _, err := s.library.Get(r.Context(), req.TrackID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "track not found", http.StatusNotFound)
			return
		}
		writeError(w, err)
		return
	}
	state := s.player.Add(s.roomID, req.TrackID)
	view, err := s.viewState(r.Context(), state)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, view)
}

func (s *Server) handleQueueRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.ID <= 0 {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	s.writeViewState(w, r, s.player.Remove(s.roomID, req.ID))
}

func (s *Server) handleQueueMove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID        int64 `json:"id"`
		Direction int   `json:"direction"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.ID <= 0 {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	if req.Direction < 0 {
		s.writeViewState(w, r, s.player.Move(s.roomID, req.ID, -1))
		return
	}
	if req.Direction > 0 {
		s.writeViewState(w, r, s.player.Move(s.roomID, req.ID, 1))
		return
	}
	s.writeViewState(w, r, s.player.Snapshot(s.roomID))
}

func (s *Server) handleQueueNext(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.ID <= 0 {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	s.writeViewState(w, r, s.player.MoveToNext(s.roomID, req.ID))
}

func (s *Server) handleQueueClear(w http.ResponseWriter, r *http.Request) {
	s.writeViewState(w, r, s.player.Clear(s.roomID))
}

func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request) {
	state, err := s.player.Play(s.roomID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	s.writeViewState(w, r, state)
}

func (s *Server) handlePlayNow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TrackID int64 `json:"track_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.TrackID <= 0 {
		http.Error(w, "track_id is required", http.StatusBadRequest)
		return
	}
	if _, err := s.library.Get(r.Context(), req.TrackID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "track not found", http.StatusNotFound)
			return
		}
		writeError(w, err)
		return
	}
	s.writeViewState(w, r, s.player.PlayNow(s.roomID, req.TrackID))
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	s.writeViewState(w, r, s.player.Pause(s.roomID))
}

func (s *Server) handleSeek(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PositionMS int64 `json:"position_ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	s.writeViewState(w, r, s.player.Seek(s.roomID, req.PositionMS))
}

func (s *Server) handleSkip(w http.ResponseWriter, r *http.Request) {
	s.writeViewState(w, r, s.player.Skip(s.roomID))
}

func (s *Server) handleEnded(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TrackID int64 `json:"track_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.TrackID <= 0 {
		http.Error(w, "track_id is required", http.StatusBadRequest)
		return
	}
	s.writeViewState(w, r, s.player.Ended(s.roomID, req.TrackID))
}

func (s *Server) handleRescan(w http.ResponseWriter, r *http.Request) {
	if err := s.scanner.Scan(r.Context()); err != nil {
		writeError(w, err)
		return
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
	track, err := s.library.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		writeError(w, err)
		return
	}
	f, err := os.Open(track.Path)
	if err != nil {
		writeError(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "audio/mpeg")
	http.ServeContent(w, r, track.Title+".mp3", track.ModTime, f)
}

func (s *Server) writeViewState(w http.ResponseWriter, r *http.Request, state PlaybackState) {
	view, err := s.viewState(r.Context(), state)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, view)
}

type ViewState struct {
	PlaybackState
	Current *Track            `json:"current"`
	Queue   []ViewQueueItem   `json:"queue"`
	History []ViewHistoryItem `json:"history"`
}

type ViewQueueItem struct {
	QueueItem
	Track *Track `json:"track"`
}

type ViewHistoryItem struct {
	PlayedItem
	Track *Track `json:"track"`
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

func writeError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
