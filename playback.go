package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
)

var (
	ErrEmptyQueue        = errors.New("queue is empty")
	ErrQueueFull         = errors.New("queue is full")
	ErrQueueItemNotFound = errors.New("queue item not found")
)

type PlaybackItem struct {
	ID          int64     `json:"id,omitempty"`
	DedupeKey   string    `json:"dedupe_key"`
	At          time.Time `json:"at"`
	RequestedBy string    `json:"requested_by"`
	Source      string    `json:"source,omitempty"`
}

const (
	AutoDJSourceLibrary  = "library"
	AutoDJSourcePlaylist = "playlist"
)

type AutoDJSource struct {
	Type       string `json:"type"`
	PlaylistID int64  `json:"playlist_id,omitempty"`
	Name       string `json:"name"`
}

type AutoDJState struct {
	Enabled bool         `json:"enabled"`
	Source  AutoDJSource `json:"source"`
}

type RoomAudio struct {
	Volume float64 `json:"volume"`
	Muted  bool    `json:"muted"`
}

type RoomAction struct {
	At       time.Time `json:"at"`
	IP       string    `json:"ip"`
	Username string    `json:"username"`
	Text     string    `json:"text"`
}

func defaultAutoDJSource() AutoDJSource {
	return AutoDJSource{Type: AutoDJSourceLibrary, Name: "Entire Library"}
}

type PlaybackState struct {
	RoomID            string         `json:"room_id"`
	Generation        string         `json:"generation"`
	Revision          uint64         `json:"revision"`
	Current           PlaybackItem   `json:"-"`
	StartedAt         time.Time      `json:"started_at"`
	Paused            bool           `json:"paused"`
	PositionAtPauseMS int64          `json:"position_at_pause_ms"`
	Queue             []PlaybackItem `json:"queue"`
	History           []PlaybackItem `json:"history"`
	Listeners         []string       `json:"listeners"`
	AutoDJ            AutoDJState    `json:"auto_dj"`
	RoomAudio         RoomAudio      `json:"room_audio"`
	Actions           []RoomAction   `json:"actions"`
	ServerTime        time.Time      `json:"server_time"`
	Disconnect        bool           `json:"-"`
}

// persistedPlayback is deliberately limited to user-visible playback intent.
// Listener state, timers, and Auto-DJ shuffle progress are process-local and
// are not persisted.
type persistedPlayback struct {
	Revision           uint64               `json:"-"`
	Current            string               `json:"current"`
	CurrentRequestedBy string               `json:"current_requested_by"`
	CurrentSource      string               `json:"current_source"`
	Started            time.Time            `json:"started"`
	Paused             bool                 `json:"paused"`
	PausePos           int64                `json:"pause_pos_ms"`
	Queue              []persistedQueueItem `json:"queue"`
	History            []persistedQueueItem `json:"history"`
	AutoDJ             AutoDJState          `json:"auto_dj"`
	RoomAudio          RoomAudio            `json:"room_audio"`
	Actions            []RoomAction         `json:"actions"`
}

type persistedQueueItem struct {
	DedupeKey   string `json:"dedupe_key"`
	RequestedBy string `json:"requested_by"`
	Source      string `json:"source"`
}

type Playback struct {
	mu                 sync.Mutex
	roomID             string
	generation         string
	nextID             int64
	revision           uint64
	endTimer           *time.Timer
	endTimerStartedAt  time.Time
	current            string
	currentRequestedBy string
	currentSource      string
	started            time.Time
	paused             bool
	pausePos           int64
	queue              []PlaybackItem
	history            []PlaybackItem
	autoDJ             AutoDJState
	autoDJNext         string
	autoDJEntries      []int64
	autoDJPreparing    bool
	roomAudio          RoomAudio
	actions            []RoomAction
	notify             map[chan PlaybackState]UserInfo
	listeners          map[string]*listenerPresence
	listenerGrace      time.Duration
	disconnected       map[string]bool
	nextListenerOrder  uint64
}

type listenerPresence struct {
	username    string
	connections int
	generation  uint64
	order       uint64
}

const defaultListenerGrace = 10 * time.Second
const maxRoomActions = 200
const maxQueueItems = 200

func NewPlayback(roomID string) *Playback {
	return &Playback{
		roomID:        roomID,
		generation:    newPlaybackGeneration(),
		autoDJ:        AutoDJState{Source: defaultAutoDJSource()},
		roomAudio:     RoomAudio{Volume: 0.25},
		notify:        make(map[chan PlaybackState]UserInfo),
		listeners:     make(map[string]*listenerPresence),
		listenerGrace: defaultListenerGrace,
	}
}

func newPlaybackGeneration() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("generate playback identity: " + err.Error())
	}
	return hex.EncodeToString(value[:])
}

func (p *Playback) Add(dedupeKey string, requestedBy string) (PlaybackState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.queue) >= maxQueueItems {
		return p.stateLocked(), ErrQueueFull
	}
	p.nextID++
	p.queue = append(p.queue, PlaybackItem{ID: p.nextID, DedupeKey: dedupeKey, At: time.Now(), RequestedBy: requestedBy, Source: "user"})
	p.bumpLocked()
	return p.stateLocked(), nil
}

func (p *Playback) Play() (PlaybackState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.current == "" {
		if !p.startNextLocked() {
			return PlaybackState{}, ErrEmptyQueue
		}
	} else if p.paused {
		p.started = time.Now().Add(-time.Duration(p.pausePos) * time.Millisecond)
		p.paused = false
		p.pausePos = 0
		p.bumpLocked()
	}
	return p.stateLocked(), nil
}

func (p *Playback) PlayNow(dedupeKey string, requestedBy string) PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.removeQueuedTrackLocked(dedupeKey)
	if p.autoDJNext == dedupeKey {
		p.autoDJNext = ""
	}
	p.recordCurrentLocked()
	p.current = dedupeKey
	p.currentRequestedBy = requestedBy
	p.currentSource = "user"
	p.started = time.Now()
	p.paused = false
	p.pausePos = 0
	p.bumpLocked()
	return p.stateLocked()
}

func (p *Playback) Pause() PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.current != "" && !p.paused {
		p.pausePos = time.Since(p.started).Milliseconds()
		if p.pausePos < 0 {
			p.pausePos = 0
		}
		p.paused = true
		p.bumpLocked()
	}
	return p.stateLocked()
}

func (p *Playback) SeekTo(positionMS int64) PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()

	if positionMS < 0 {
		positionMS = 0
	}
	if p.current != "" {
		if p.paused {
			p.pausePos = positionMS
		} else {
			p.started = time.Now().Add(-time.Duration(positionMS) * time.Millisecond)
		}
		p.bumpLocked()
	}
	return p.stateLocked()
}

func (p *Playback) Skip() PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.startNextLocked()
	return p.stateLocked()
}

func (p *Playback) Previous() PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.history) == 0 {
		return p.stateLocked()
	}
	item := p.history[0]
	p.history = p.history[1:]
	if p.autoDJNext == item.DedupeKey {
		p.autoDJNext = ""
	}
	if p.current != "" {
		p.nextID++
		p.queue = append([]PlaybackItem{{ID: p.nextID, DedupeKey: p.current, At: time.Now(), RequestedBy: p.currentRequestedBy, Source: p.currentSource}}, p.queue...)
	}
	p.current = item.DedupeKey
	p.currentRequestedBy = item.RequestedBy
	p.currentSource = item.Source
	p.started = time.Now()
	p.paused = false
	p.pausePos = 0
	p.bumpLocked()
	return p.stateLocked()
}

func (p *Playback) Ended(dedupeKey string) PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.current != "" && p.current == dedupeKey {
		p.startNextLocked()
	}
	return p.stateLocked()
}

func (p *Playback) endScheduled(dedupeKey string, startedAt time.Time) (PlaybackState, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.paused || p.current != dedupeKey || !p.started.Equal(startedAt) {
		return p.stateLocked(), false
	}
	p.endTimer = nil
	p.endTimerStartedAt = time.Time{}
	p.startNextLocked()
	return p.stateLocked(), true
}

func (p *Playback) endTimerMatches(dedupeKey string, startedAt time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.endTimer != nil && p.current == dedupeKey && p.started.Equal(startedAt) && p.endTimerStartedAt.Equal(startedAt)
}

func (p *Playback) scheduleEnd(after time.Duration, dedupeKey string, startedAt time.Time, callback func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.paused || p.current != dedupeKey || !p.started.Equal(startedAt) {
		return
	}
	if p.endTimer != nil {
		p.endTimer.Stop()
	}
	p.endTimerStartedAt = startedAt
	p.endTimer = time.AfterFunc(after, callback)
}

func (p *Playback) cancelEndTimer() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.endTimer != nil {
		p.endTimer.Stop()
	}
	p.endTimer = nil
	p.endTimerStartedAt = time.Time{}
}

func (p *Playback) Remove(queueItemID int64) PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i, item := range p.queue {
		if item.ID == queueItemID {
			p.queue = append(p.queue[:i], p.queue[i+1:]...)
			p.bumpLocked()
			break
		}
	}
	return p.stateLocked()
}

func (p *Playback) Reorder(queueItemID, beforeQueueItemID int64) (PlaybackState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	sourceIndex := -1
	for i, item := range p.queue {
		if item.ID == queueItemID {
			sourceIndex = i
			break
		}
	}
	if sourceIndex < 0 {
		return p.stateLocked(), ErrQueueItemNotFound
	}
	if queueItemID == beforeQueueItemID {
		return p.stateLocked(), nil
	}

	next := make([]PlaybackItem, 0, len(p.queue))
	next = append(next, p.queue[:sourceIndex]...)
	next = append(next, p.queue[sourceIndex+1:]...)
	insertIndex := len(next)
	if beforeQueueItemID != 0 {
		insertIndex = -1
		for i, item := range next {
			if item.ID == beforeQueueItemID {
				insertIndex = i
				break
			}
		}
		if insertIndex < 0 {
			return p.stateLocked(), ErrQueueItemNotFound
		}
	}

	next = append(next, PlaybackItem{})
	copy(next[insertIndex+1:], next[insertIndex:])
	next[insertIndex] = p.queue[sourceIndex]
	if slices.EqualFunc(p.queue, next, func(a, b PlaybackItem) bool { return a.ID == b.ID }) {
		return p.stateLocked(), nil
	}
	p.queue = next
	p.bumpLocked()
	return p.stateLocked(), nil
}

func (p *Playback) Clear() PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.queue) > 0 {
		p.queue = nil
		p.bumpLocked()
	}
	return p.stateLocked()
}

func (p *Playback) ClearHistory() PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.history) > 0 {
		p.history = nil
		p.bumpLocked()
	}
	return p.stateLocked()
}

func (p *Playback) SetRoomAudio(volume float64, muted bool) PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.roomAudio = RoomAudio{Volume: volume, Muted: muted}
	p.bumpLocked()
	return p.stateLocked()
}

func (p *Playback) Snapshot() PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stateLocked()
}

func (p *Playback) PersistentState() persistedPlayback {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.persistentStateLocked()
}

func (p *Playback) RestorePersistentState(state persistedPlayback, revision uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.revision = revision
	p.nextID = 0
	p.current = state.Current
	p.currentRequestedBy = state.CurrentRequestedBy
	p.currentSource = state.CurrentSource
	p.started = state.Started
	p.paused = state.Paused
	p.pausePos = state.PausePos
	p.queue = make([]PlaybackItem, 0, len(state.Queue))
	for _, item := range state.Queue {
		if item.DedupeKey == "" {
			continue
		}
		p.nextID++
		p.queue = append(p.queue, PlaybackItem{
			ID:          p.nextID,
			DedupeKey:   item.DedupeKey,
			RequestedBy: item.RequestedBy,
			Source:      item.Source,
		})
	}
	p.history = make([]PlaybackItem, 0, len(state.History))
	for _, item := range state.History {
		if item.DedupeKey == "" {
			continue
		}
		p.history = append(p.history, PlaybackItem{
			DedupeKey:   item.DedupeKey,
			RequestedBy: item.RequestedBy,
			Source:      item.Source,
		})
	}
	p.autoDJ = state.AutoDJ
	if p.autoDJ.Source.Type == "" {
		p.autoDJ.Source = defaultAutoDJSource()
	}
	p.autoDJNext = ""
	p.autoDJEntries = nil
	p.autoDJPreparing = false
	p.roomAudio = state.RoomAudio
	p.actions = append([]RoomAction(nil), state.Actions...)
}

func (p *Playback) AddAction(action RoomAction) PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()
	if action.At.IsZero() {
		action.At = time.Now()
	}
	p.actions = append([]RoomAction{action}, p.actions...)
	if len(p.actions) > maxRoomActions {
		p.actions = p.actions[:maxRoomActions]
	}
	p.bumpLocked()
	return p.stateLocked()
}

func (p *Playback) ConfigureAutoDJ(enabled bool, candidate string, entries []int64) PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.autoDJ.Enabled = enabled
	if enabled {
		p.autoDJNext = candidate
		p.autoDJEntries = entries
	} else {
		p.autoDJNext = ""
		p.autoDJEntries = nil
	}
	p.autoDJPreparing = false
	p.bumpLocked()
	return p.stateLocked()
}

func (p *Playback) ConfigureAutoDJForSource(source AutoDJSource, enabled bool, candidate string, entries []int64) (PlaybackState, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.autoDJ.Source != source {
		return p.stateLocked(), false
	}
	p.autoDJ.Enabled = enabled
	if enabled {
		p.autoDJNext = candidate
		p.autoDJEntries = entries
	} else {
		p.autoDJNext = ""
		p.autoDJEntries = nil
	}
	p.autoDJPreparing = false
	p.bumpLocked()
	return p.stateLocked(), true
}

func (p *Playback) ConfigureAutoDJSource(source AutoDJSource, candidate string, entries []int64) PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.autoDJ.Source = source
	if p.autoDJ.Enabled {
		p.autoDJNext = candidate
		p.autoDJEntries = entries
	} else {
		p.autoDJNext = ""
		p.autoDJEntries = nil
	}
	p.autoDJPreparing = false
	p.bumpLocked()
	return p.stateLocked()
}

func (p *Playback) AutoDJConfiguration() (AutoDJState, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.autoDJ, p.autoDJNext
}

func (p *Playback) BeginAutoDJCandidate(source AutoDJSource) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.autoDJ.Enabled || p.autoDJ.Source != source || p.autoDJNext != "" || p.autoDJPreparing {
		return false
	}
	p.autoDJPreparing = true
	return true
}

func (p *Playback) CompleteAutoDJCandidate(source AutoDJSource, candidate string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.autoDJ.Enabled || p.autoDJ.Source != source || !p.autoDJPreparing {
		return false
	}
	p.autoDJPreparing = false
	if p.autoDJNext == "" {
		p.autoDJNext = candidate
	}
	return true
}

func (p *Playback) ClearAutoDJCandidate(source AutoDJSource) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.autoDJ.Enabled || p.autoDJ.Source != source {
		return false
	}
	p.autoDJNext = ""
	p.autoDJPreparing = false
	return true
}

func (p *Playback) TakeAutoDJEntry(source AutoDJSource) (int64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.autoDJ.Enabled || p.autoDJ.Source != source || !p.autoDJPreparing || len(p.autoDJEntries) == 0 {
		return 0, false
	}
	last := len(p.autoDJEntries) - 1
	id := p.autoDJEntries[last]
	p.autoDJEntries = p.autoDJEntries[:last]
	return id, true
}

func (p *Playback) RefillAutoDJEntries(source AutoDJSource, entries []int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.autoDJ.Enabled || p.autoDJ.Source != source || !p.autoDJPreparing || len(p.autoDJEntries) != 0 {
		return false
	}
	p.autoDJEntries = entries
	return true
}

func (p *Playback) ResetAutoDJPlaylistSource(playlistID int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.autoDJ.Source.Type != AutoDJSourcePlaylist || p.autoDJ.Source.PlaylistID != playlistID {
		return false
	}
	p.autoDJ = AutoDJState{Source: defaultAutoDJSource()}
	p.autoDJNext = ""
	p.autoDJEntries = nil
	p.autoDJPreparing = false
	p.bumpLocked()
	return true
}

func (p *Playback) InvalidateAutoDJPlaylistCandidate(playlistID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.autoDJ.Source.Type == AutoDJSourcePlaylist && p.autoDJ.Source.PlaylistID == playlistID {
		p.autoDJNext = ""
		p.autoDJPreparing = false
	}
}

func (p *Playback) Notify() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bumpLocked()
}

func (p *Playback) Subscribe(listener UserInfo) (<-chan PlaybackState, func()) {
	ch, cancel, _ := p.SubscribeIfAllowed(listener)
	return ch, cancel
}

func (p *Playback) SubscribeIfAllowed(listener UserInfo) (<-chan PlaybackState, func(), bool) {
	ch := make(chan PlaybackState, 1)
	p.mu.Lock()
	if p.disconnected[listenerIdentity(listener)] {
		close(ch)
		p.mu.Unlock()
		return ch, func() {}, false
	}
	if p.notify == nil {
		p.notify = make(map[chan PlaybackState]UserInfo)
	}
	p.notify[ch] = listener
	p.listenerJoinedLocked(listener)
	ch <- p.stateLocked()
	p.mu.Unlock()

	return ch, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if listener, ok := p.notify[ch]; ok {
			delete(p.notify, ch)
			close(ch)
			p.listenerDepartedLocked(listener)
		}
	}, true
}

func (p *Playback) ListenerDisconnected(listener UserInfo) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.disconnected[listenerIdentity(listener)]
}

func (p *Playback) DisconnectListener(username string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	username = strings.TrimSpace(username)
	if p.disconnected == nil {
		p.disconnected = make(map[string]bool)
	}
	disconnected := false
	disconnectedUsers := make(map[string]struct{})
	for ch, listener := range p.notify {
		if !strings.EqualFold(listener.Display(), username) {
			continue
		}
		disconnected = true
		p.disconnected[listenerIdentity(listener)] = true
		disconnectedUsers[listenerUserIdentity(listener)] = struct{}{}
		delete(p.notify, ch)
		for len(ch) > 0 {
			<-ch
		}
		ch <- PlaybackState{RoomID: p.roomID, Disconnect: true}
		close(ch)
	}
	if disconnected {
		for identity := range disconnectedUsers {
			delete(p.listeners, identity)
		}
		p.bumpLocked()
	}
	return disconnected
}

func (p *Playback) CloseSubscribers() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.endTimer != nil {
		p.endTimer.Stop()
		p.endTimer = nil
	}
	p.endTimerStartedAt = time.Time{}

	for ch := range p.notify {
		delete(p.notify, ch)
		close(ch)
	}
	clear(p.listeners)
}

func (p *Playback) startNextLocked() bool {
	if len(p.queue) == 0 {
		if p.autoDJ.Enabled && p.autoDJNext != "" {
			p.recordCurrentLocked()
			p.current = p.autoDJNext
			p.currentRequestedBy = ""
			p.currentSource = "auto_dj"
			p.autoDJNext = ""
			p.started = time.Now()
			p.paused = false
			p.pausePos = 0
			p.bumpLocked()
			return true
		}
		p.recordCurrentLocked()
		p.current = ""
		p.currentRequestedBy = ""
		p.currentSource = ""
		p.started = time.Time{}
		p.paused = false
		p.pausePos = 0
		p.bumpLocked()
		return false
	}
	item := p.queue[0]
	p.queue = p.queue[1:]
	if p.autoDJNext == item.DedupeKey {
		p.autoDJNext = ""
	}
	p.recordCurrentLocked()
	p.current = item.DedupeKey
	p.currentRequestedBy = item.RequestedBy
	p.currentSource = item.Source
	p.started = time.Now()
	p.paused = false
	p.pausePos = 0
	p.bumpLocked()
	return true
}

func (p *Playback) removeQueuedTrackLocked(dedupeKey string) {
	for i, item := range p.queue {
		if item.DedupeKey == dedupeKey {
			p.queue = append(p.queue[:i], p.queue[i+1:]...)
			return
		}
	}
}

func (p *Playback) recordCurrentLocked() {
	if p.current == "" {
		return
	}
	p.history = append([]PlaybackItem{{DedupeKey: p.current, At: time.Now(), RequestedBy: p.currentRequestedBy, Source: p.currentSource}}, p.history...)
	if len(p.history) > 25 {
		p.history = p.history[:25]
	}
}

func (p *Playback) bumpLocked() {
	p.revision++
	state := p.stateLocked()
	for ch := range p.notify {
		select {
		case ch <- state:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- state:
			default:
			}
		}
	}
}

func (p *Playback) stateLocked() PlaybackState {
	queue := append([]PlaybackItem(nil), p.queue...)
	history := append([]PlaybackItem(nil), p.history...)
	actions := append([]RoomAction(nil), p.actions...)
	listeners := p.listenersLocked()
	return PlaybackState{
		RoomID:            p.roomID,
		Generation:        p.generation,
		Revision:          p.revision,
		Current:           PlaybackItem{DedupeKey: p.current, At: p.started, RequestedBy: p.currentRequestedBy, Source: p.currentSource},
		StartedAt:         p.started,
		Paused:            p.paused,
		PositionAtPauseMS: p.pausePos,
		Queue:             queue,
		History:           history,
		Listeners:         listeners,
		AutoDJ:            p.autoDJ,
		RoomAudio:         p.roomAudio,
		Actions:           actions,
		ServerTime:        time.Now(),
	}
}

func (p *Playback) persistentStateLocked() persistedPlayback {
	return persistedPlayback{
		Revision:           p.revision,
		Current:            p.current,
		CurrentRequestedBy: p.currentRequestedBy,
		CurrentSource:      p.currentSource,
		Started:            p.started,
		Paused:             p.paused,
		PausePos:           p.pausePos,
		Queue:              persistedQueue(p.queue),
		History:            persistedQueue(p.history),
		AutoDJ:             p.autoDJ,
		RoomAudio:          p.roomAudio,
		Actions:            append([]RoomAction(nil), p.actions...),
	}
}

func persistedQueue(queue []PlaybackItem) []persistedQueueItem {
	persisted := make([]persistedQueueItem, 0, len(queue))
	for _, item := range queue {
		persisted = append(persisted, persistedQueueItem{
			DedupeKey:   item.DedupeKey,
			RequestedBy: item.RequestedBy,
			Source:      item.Source,
		})
	}
	return persisted
}

func (p *Playback) listenersLocked() []string {
	type displayName struct {
		username string
		order    uint64
	}
	seen := make(map[string]displayName, len(p.listeners))
	for _, listener := range p.listeners {
		username := strings.TrimSpace(listener.username)
		if username == "" {
			continue
		}
		key := strings.ToLower(username)
		if existing, ok := seen[key]; !ok || listener.order < existing.order {
			seen[key] = displayName{username: username, order: listener.order}
		}
	}
	listeners := make([]string, 0, len(seen))
	for _, display := range seen {
		listeners = append(listeners, display.username)
	}
	slices.Sort(listeners)
	return listeners
}

func (p *Playback) listenerJoinedLocked(listener UserInfo) {
	if p.listeners == nil {
		p.listeners = make(map[string]*listenerPresence)
	}
	identity := listenerUserIdentity(listener)
	presence := p.listeners[identity]
	if presence == nil {
		p.nextListenerOrder++
		presence = &listenerPresence{order: p.nextListenerOrder}
		p.listeners[identity] = presence
	}
	presence.username = listener.Display()
	presence.connections++
	presence.generation++
}

func (p *Playback) listenerDepartedLocked(listener UserInfo) {
	identity := listenerUserIdentity(listener)
	presence := p.listeners[identity]
	if presence == nil || presence.connections == 0 {
		return
	}
	presence.connections--
	presence.generation++
	if presence.connections > 0 {
		return
	}
	generation := presence.generation
	remove := func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		current := p.listeners[identity]
		if current != presence || current.connections != 0 || current.generation != generation {
			return
		}
		delete(p.listeners, identity)
		p.bumpLocked()
	}
	if p.listenerGrace <= 0 {
		delete(p.listeners, identity)
		p.bumpLocked()
		return
	}
	time.AfterFunc(p.listenerGrace, remove)
}

func listenerUserIdentity(listener UserInfo) string {
	if listener.ID != "" {
		return "id:" + listener.ID
	}
	return "username:" + strings.ToLower(strings.TrimSpace(listener.Username))
}

func listenerIdentity(listener UserInfo) string {
	if listener.SessionKey != "" {
		return listener.SessionKey
	}
	if listener.ID != "" {
		return "id:" + listener.ID
	}
	return "username:" + listener.Username
}
