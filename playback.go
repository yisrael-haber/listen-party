package main

import (
	"errors"
	"slices"
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
	ServerTime        time.Time      `json:"server_time"`
}

type Playback struct {
	mu                 sync.Mutex
	roomID             string
	nextID             int64
	current            string
	currentRequestedBy string
	started            time.Time
	paused             bool
	pausePos           int64
	queue              []PlaybackItem
	history            []PlaybackItem
	notify             map[chan PlaybackState]UserInfo
}

func NewPlayback(roomID string) *Playback {
	return &Playback{
		roomID: roomID,
		notify: make(map[chan PlaybackState]UserInfo),
	}
}

func (p *Playback) Add(dedupeKey string, requestedBy string) PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.nextID++
	p.queue = append(p.queue, PlaybackItem{ID: p.nextID, DedupeKey: dedupeKey, At: time.Now(), RequestedBy: requestedBy})
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
		p.queue = append(p.queue, PlaybackItem{ID: p.nextID, DedupeKey: dedupeKey, At: now, RequestedBy: requestedBy})
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
	p.recordCurrentLocked()
	p.current = dedupeKey
	p.currentRequestedBy = requestedBy
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
	if p.current != "" {
		p.nextID++
		p.queue = append([]PlaybackItem{{ID: p.nextID, DedupeKey: p.current, At: time.Now(), RequestedBy: p.currentRequestedBy}}, p.queue...)
	}
	p.current = item.DedupeKey
	p.currentRequestedBy = item.RequestedBy
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

func (p *Playback) Notify() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bumpLocked()
}

func (p *Playback) Subscribe(listener UserInfo) (<-chan PlaybackState, func()) {
	ch := make(chan PlaybackState, 8)
	p.mu.Lock()
	if p.notify == nil {
		p.notify = make(map[chan PlaybackState]UserInfo)
	}
	p.notify[ch] = listener
	ch <- p.stateLocked()
	p.mu.Unlock()

	return ch, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if _, ok := p.notify[ch]; ok {
			delete(p.notify, ch)
			close(ch)
		}
	}
}

func (p *Playback) CloseSubscribers() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for ch := range p.notify {
		delete(p.notify, ch)
		close(ch)
	}
}

func (p *Playback) startNextLocked() bool {
	if len(p.queue) == 0 {
		p.recordCurrentLocked()
		p.current = ""
		p.currentRequestedBy = ""
		p.started = time.Time{}
		p.paused = false
		p.pausePos = 0
		p.bumpLocked()
		return false
	}
	item := p.queue[0]
	p.queue = p.queue[1:]
	p.recordCurrentLocked()
	p.current = item.DedupeKey
	p.currentRequestedBy = item.RequestedBy
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
	p.history = append([]PlaybackItem{{DedupeKey: p.current, At: time.Now(), RequestedBy: p.currentRequestedBy}}, p.history...)
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
		Current:           PlaybackItem{DedupeKey: p.current, At: p.started, RequestedBy: p.currentRequestedBy},
		StartedAt:         p.started,
		Paused:            p.paused,
		PositionAtPauseMS: p.pausePos,
		Queue:             queue,
		History:           history,
		Listeners:         listeners,
		ServerTime:        time.Now(),
	}
}

func (p *Playback) listenersLocked() []string {
	seen := make(map[string]string, len(p.notify))
	for _, listener := range p.notify {
		key := listener.ID
		if key == "" {
			key = listener.Username
		}
		if key == "" {
			continue
		}
		if _, ok := seen[key]; !ok {
			seen[key] = listener.Username
		}
	}
	listeners := make([]string, 0, len(seen))
	for _, username := range seen {
		listeners = append(listeners, username)
	}
	slices.Sort(listeners)
	return listeners
}
