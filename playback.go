package main

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
)

var (
	ErrEmptyQueue        = errors.New("queue is empty")
	ErrQueueItemNotFound = errors.New("queue item not found")
)

type PlaybackItem struct {
	ID          int64     `json:"id,omitempty"`
	DedupeKey   string    `json:"dedupe_key"`
	At          time.Time `json:"at"`
	RequestedBy string    `json:"requested_by"`
	Source      string    `json:"source,omitempty"`
}

type PlaybackState struct {
	RoomID            string         `json:"room_id"`
	Current           PlaybackItem   `json:"-"`
	StartedAt         time.Time      `json:"started_at"`
	Paused            bool           `json:"paused"`
	PositionAtPauseMS int64          `json:"position_at_pause_ms"`
	Queue             []PlaybackItem `json:"queue"`
	History           []PlaybackItem `json:"history"`
	Listeners         []string       `json:"listeners"`
	AutoDJEnabled     bool           `json:"auto_dj_enabled"`
	ServerTime        time.Time      `json:"server_time"`
	Disconnect        bool           `json:"-"`
}

type Playback struct {
	mu                 sync.Mutex
	roomID             string
	nextID             int64
	current            string
	currentRequestedBy string
	currentSource      string
	started            time.Time
	paused             bool
	pausePos           int64
	queue              []PlaybackItem
	history            []PlaybackItem
	autoDJEnabled      bool
	autoDJNext         string
	notify             map[chan PlaybackState]UserInfo
	listeners          map[string]*listenerPresence
	listenerGrace      time.Duration
	disconnected       map[string]bool
}

type listenerPresence struct {
	username    string
	connections int
	generation  uint64
}

const defaultListenerGrace = 10 * time.Second

func NewPlayback(roomID string) *Playback {
	return &Playback{
		roomID:        roomID,
		notify:        make(map[chan PlaybackState]UserInfo),
		listeners:     make(map[string]*listenerPresence),
		listenerGrace: defaultListenerGrace,
	}
}

func (p *Playback) Add(dedupeKey string, requestedBy string) PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.nextID++
	p.queue = append(p.queue, PlaybackItem{ID: p.nextID, DedupeKey: dedupeKey, At: time.Now(), RequestedBy: requestedBy, Source: "user"})
	p.bumpLocked()
	return p.stateLocked()
}

func (p *Playback) AddMany(dedupeKeys []string, requestedBy string) PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	for _, dedupeKey := range dedupeKeys {
		if dedupeKey == "" {
			continue
		}
		p.nextID++
		p.queue = append(p.queue, PlaybackItem{ID: p.nextID, DedupeKey: dedupeKey, At: now, RequestedBy: requestedBy, Source: "user"})
	}
	p.bumpLocked()
	return p.stateLocked()
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

func (p *Playback) Snapshot() PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stateLocked()
}

func (p *Playback) ConfigureAutoDJ(enabled bool, candidate string) PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.autoDJEnabled = enabled
	if enabled {
		p.autoDJNext = candidate
	} else {
		p.autoDJNext = ""
	}
	p.bumpLocked()
	return p.stateLocked()
}

func (p *Playback) AutoDJCandidate() (bool, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.autoDJEnabled, p.autoDJNext
}

func (p *Playback) SetAutoDJCandidateIfEmpty(candidate string) PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.autoDJEnabled && p.autoDJNext == "" {
		p.autoDJNext = candidate
	}
	return p.stateLocked()
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
	ch := make(chan PlaybackState, 8)
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
		if !strings.EqualFold(strings.TrimSpace(listener.Username), username) {
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

	for ch := range p.notify {
		delete(p.notify, ch)
		close(ch)
	}
	clear(p.listeners)
}

func (p *Playback) startNextLocked() bool {
	if len(p.queue) == 0 {
		if p.autoDJEnabled && p.autoDJNext != "" {
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
	state := p.stateLocked()
	for ch := range p.notify {
		select {
		case ch <- state:
		default:
		}
	}
}

func (p *Playback) stateLocked() PlaybackState {
	queue := append([]PlaybackItem(nil), p.queue...)
	history := append([]PlaybackItem(nil), p.history...)
	listeners := p.listenersLocked()
	return PlaybackState{
		RoomID:            p.roomID,
		Current:           PlaybackItem{DedupeKey: p.current, At: p.started, RequestedBy: p.currentRequestedBy, Source: p.currentSource},
		StartedAt:         p.started,
		Paused:            p.paused,
		PositionAtPauseMS: p.pausePos,
		Queue:             queue,
		History:           history,
		Listeners:         listeners,
		AutoDJEnabled:     p.autoDJEnabled,
		ServerTime:        time.Now(),
	}
}

func (p *Playback) listenersLocked() []string {
	seen := make(map[string]string, len(p.listeners))
	for _, listener := range p.listeners {
		username := strings.TrimSpace(listener.username)
		if username == "" {
			continue
		}
		key := strings.ToLower(username)
		if _, ok := seen[key]; !ok {
			seen[key] = username
		}
	}
	listeners := make([]string, 0, len(seen))
	for _, username := range seen {
		listeners = append(listeners, username)
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
		presence = &listenerPresence{}
		p.listeners[identity] = presence
	}
	presence.username = listener.Username
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
