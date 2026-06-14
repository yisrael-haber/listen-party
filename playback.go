package main

import (
	"errors"
	"slices"
	"sync"
	"time"
)

var ErrEmptyQueue = errors.New("queue is empty")

type QueueItem struct {
	ID          int64     `json:"id"`
	TrackID     int64     `json:"track_id"`
	AddedAt     time.Time `json:"added_at"`
	RequestedBy string    `json:"requested_by"`
}

type PlayedItem struct {
	TrackID     int64     `json:"track_id"`
	PlayedAt    time.Time `json:"played_at"`
	RequestedBy string    `json:"requested_by"`
}

type PlaybackState struct {
	RoomID             string       `json:"room_id"`
	Revision           int64        `json:"revision"`
	PlaybackID         int64        `json:"playback_id"`
	CurrentTrackID     int64        `json:"current_track_id"`
	CurrentRequestedBy string       `json:"current_requested_by"`
	StartedAt          time.Time    `json:"started_at"`
	Paused             bool         `json:"paused"`
	PositionAtPauseMS  int64        `json:"position_at_pause_ms"`
	Queue              []QueueItem  `json:"queue"`
	History            []PlayedItem `json:"history"`
	ListenerCount      int          `json:"listener_count"`
	Listeners          []string     `json:"listeners"`
	ServerTime         time.Time    `json:"server_time"`
}

type ActiveListener struct {
	UserID   string
	Username string
}

type Playback struct {
	mu                 sync.Mutex
	roomID             string
	nextID             int64
	rev                int64
	playID             int64
	current            int64
	currentRequestedBy string
	started            time.Time
	paused             bool
	pausePos           int64
	queue              []QueueItem
	history            []PlayedItem
	notify             map[chan PlaybackState]ActiveListener
}

func NewPlayback(roomID string) *Playback {
	return &Playback{
		roomID: roomID,
		notify: make(map[chan PlaybackState]ActiveListener),
	}
}

func (p *Playback) Add(trackID int64, requestedBy string) PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.nextID++
	p.queue = append(p.queue, QueueItem{ID: p.nextID, TrackID: trackID, AddedAt: time.Now(), RequestedBy: requestedBy})
	p.bumpLocked()
	return p.stateLocked()
}

func (p *Playback) Play() (PlaybackState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.current == 0 {
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

func (p *Playback) PlayNow(trackID int64, requestedBy string) PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.removeQueuedTrackLocked(trackID)
	p.recordCurrentLocked()
	p.playID++
	p.current = trackID
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

	if p.current != 0 && !p.paused {
		p.pausePos = time.Since(p.started).Milliseconds()
		if p.pausePos < 0 {
			p.pausePos = 0
		}
		p.paused = true
		p.bumpLocked()
	}
	return p.stateLocked()
}

func (p *Playback) Seek(positionMS int64) PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()

	if positionMS < 0 {
		positionMS = 0
	}
	if p.current != 0 {
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
	if p.current != 0 {
		p.nextID++
		p.queue = append([]QueueItem{{ID: p.nextID, TrackID: p.current, AddedAt: time.Now(), RequestedBy: p.currentRequestedBy}}, p.queue...)
	}
	p.playID++
	p.current = item.TrackID
	p.currentRequestedBy = item.RequestedBy
	p.started = time.Now()
	p.paused = false
	p.pausePos = 0
	p.bumpLocked()
	return p.stateLocked()
}

func (p *Playback) Ended(trackID int64) PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.current != 0 && p.current == trackID {
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

func (p *Playback) Move(queueItemID int64, delta int) PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i, item := range p.queue {
		if item.ID != queueItemID {
			continue
		}
		j := i + delta
		if j < 0 || j >= len(p.queue) {
			return p.stateLocked()
		}
		p.queue[i], p.queue[j] = p.queue[j], p.queue[i]
		p.bumpLocked()
		return p.stateLocked()
	}
	return p.stateLocked()
}

func (p *Playback) MoveToNext(queueItemID int64) PlaybackState {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i, item := range p.queue {
		if item.ID != queueItemID {
			continue
		}
		if i == 0 {
			return p.stateLocked()
		}
		copy(p.queue[1:i+1], p.queue[0:i])
		p.queue[0] = item
		p.bumpLocked()
		return p.stateLocked()
	}
	return p.stateLocked()
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

func (p *Playback) Subscribe(listener ActiveListener) (<-chan PlaybackState, func()) {
	ch := make(chan PlaybackState, 8)
	p.mu.Lock()
	if p.notify == nil {
		p.notify = make(map[chan PlaybackState]ActiveListener)
	}
	p.notify[ch] = listener
	p.bumpLocked()
	p.mu.Unlock()

	return ch, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if _, ok := p.notify[ch]; ok {
			delete(p.notify, ch)
			close(ch)
			p.bumpLocked()
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
	p.bumpLocked()
}

func (p *Playback) startNextLocked() bool {
	if len(p.queue) == 0 {
		p.recordCurrentLocked()
		p.current = 0
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
	p.playID++
	p.current = item.TrackID
	p.currentRequestedBy = item.RequestedBy
	p.started = time.Now()
	p.paused = false
	p.pausePos = 0
	p.bumpLocked()
	return true
}

func (p *Playback) removeQueuedTrackLocked(trackID int64) {
	for i, item := range p.queue {
		if item.TrackID == trackID {
			p.queue = append(p.queue[:i], p.queue[i+1:]...)
			return
		}
	}
}

func (p *Playback) recordCurrentLocked() {
	if p.current == 0 {
		return
	}
	p.history = append([]PlayedItem{{TrackID: p.current, PlayedAt: time.Now(), RequestedBy: p.currentRequestedBy}}, p.history...)
	if len(p.history) > 25 {
		p.history = p.history[:25]
	}
}

func (p *Playback) bumpLocked() {
	p.rev++
	state := p.stateLocked()
	for ch := range p.notify {
		select {
		case ch <- state:
		default:
		}
	}
}

func (p *Playback) stateLocked() PlaybackState {
	queue := append([]QueueItem(nil), p.queue...)
	history := append([]PlayedItem(nil), p.history...)
	listeners := p.listenersLocked()
	return PlaybackState{
		RoomID:             p.roomID,
		Revision:           p.rev,
		PlaybackID:         p.playID,
		CurrentTrackID:     p.current,
		CurrentRequestedBy: p.currentRequestedBy,
		StartedAt:          p.started,
		Paused:             p.paused,
		PositionAtPauseMS:  p.pausePos,
		Queue:              queue,
		History:            history,
		ListenerCount:      len(listeners),
		Listeners:          listeners,
		ServerTime:         time.Now(),
	}
}

func (p *Playback) listenersLocked() []string {
	seen := make(map[string]string, len(p.notify))
	for _, listener := range p.notify {
		key := listener.UserID
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
