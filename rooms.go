package main

import (
	"slices"
	"sync"
)

type Room struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Public        bool      `json:"public"`
	AllowedRoles  []string  `json:"allowed_roles,omitempty"`
	AllowedGroups []string  `json:"allowed_groups,omitempty"`
	Playback      *Playback `json:"-"`
}

type RoomManager struct {
	mu        sync.RWMutex
	rooms     map[string]*Room
	order     []string
	defaultID string
}

func NewRoomManager(configs []RoomConfig) *RoomManager {
	m := &RoomManager{}
	m.Update(configs)
	return m
}

func (m *RoomManager) Update(configs []RoomConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	old := m.rooms
	next := make(map[string]*Room, len(configs))
	order := make([]string, 0, len(configs))
	defaultID := ""
	for _, cfg := range configs {
		playback := (*Playback)(nil)
		if old != nil && old[cfg.ID] != nil {
			playback = old[cfg.ID].Playback
		}
		if playback == nil {
			playback = NewPlayback(cfg.ID)
		}
		room := &Room{
			ID:            cfg.ID,
			Name:          cfg.Name,
			Public:        cfg.Public,
			AllowedRoles:  append([]string(nil), cfg.AllowedRoles...),
			AllowedGroups: append([]string(nil), cfg.AllowedGroups...),
			Playback:      playback,
		}
		next[cfg.ID] = room
		order = append(order, cfg.ID)
		if defaultID == "" && cfg.Public {
			defaultID = cfg.ID
		}
	}
	for id, room := range old {
		if _, ok := next[id]; !ok {
			room.Playback.CloseSubscribers()
		}
	}
	if defaultID == "" && len(order) > 0 {
		defaultID = order[0]
	}
	m.rooms = next
	m.order = order
	m.defaultID = defaultID
}

func (m *RoomManager) DefaultID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defaultID
}

func (m *RoomManager) Get(id string) (*Room, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	room, ok := m.rooms[id]
	return room, ok
}

func (m *RoomManager) List() []Room {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rooms := make([]Room, 0, len(m.order))
	for _, id := range m.order {
		room := m.rooms[id]
		rooms = append(rooms, Room{
			ID:            room.ID,
			Name:          room.Name,
			Public:        room.Public,
			AllowedRoles:  append([]string(nil), room.AllowedRoles...),
			AllowedGroups: append([]string(nil), room.AllowedGroups...),
		})
	}
	return rooms
}

func UserCanAccessRoom(user UserInfo, room Room) bool {
	if user.Role == RoleAdmin {
		return true
	}
	if room.Public {
		return true
	}
	if slices.Contains(room.AllowedRoles, string(user.Role)) {
		return true
	}
	for _, id := range user.RoomIDs {
		if id == room.ID {
			return true
		}
	}
	for _, group := range user.Groups {
		if slices.Contains(room.AllowedGroups, group) {
			return true
		}
	}
	return false
}
