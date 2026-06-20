package main

import (
	"slices"
	"sync"
)

type RoomPermission string

const (
	PermissionQueueAdd        RoomPermission = "queue_add"
	PermissionQueueManage     RoomPermission = "queue_manage"
	PermissionPlaybackControl RoomPermission = "playback_control"
	EveryoneRoomGrant                        = "everyone"
)

var roomPermissions = []RoomPermission{
	PermissionQueueAdd,
	PermissionQueueManage,
	PermissionPlaybackControl,
}

type Room struct {
	ID       string                      `json:"id"`
	Name     string                      `json:"name"`
	Grants   map[string][]RoomPermission `json:"grants,omitempty"`
	Playback *Playback                   `json:"-"`
}

type RoomManager struct {
	mu        sync.RWMutex
	rooms     map[string]*Room
	order     []string
	defaultID string
}

func NewRoomManager(configs []Room) *RoomManager {
	m := &RoomManager{}
	m.Update(configs)
	return m
}

func (m *RoomManager) Update(configs []Room) {
	m.mu.Lock()
	defer m.mu.Unlock()

	old := m.rooms
	next := make(map[string]*Room, len(configs))
	order := make([]string, 0, len(configs))
	for _, cfg := range configs {
		playback := (*Playback)(nil)
		if old != nil && old[cfg.ID] != nil {
			playback = old[cfg.ID].Playback
		}
		if playback == nil {
			playback = NewPlayback(cfg.ID)
		}
		next[cfg.ID] = &Room{
			ID:       cfg.ID,
			Name:     cfg.Name,
			Grants:   cloneRoomGrants(cfg.Grants),
			Playback: playback,
		}
		order = append(order, cfg.ID)
	}
	for id, room := range old {
		if _, ok := next[id]; !ok {
			room.Playback.CloseSubscribers()
		}
	}
	m.rooms = next
	m.order = order
	if len(order) > 0 {
		m.defaultID = order[0]
	} else {
		m.defaultID = ""
	}
	for _, room := range next {
		room.Playback.Notify()
	}
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

func (m *RoomManager) UserHasPermission(id string, user UserInfo, permission RoomPermission) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	room, ok := m.rooms[id]
	return ok && UserHasRoomPermission(user, *room, permission)
}

func (m *RoomManager) PermissionsForUser(id string, user UserInfo) ([]RoomPermission, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	room, ok := m.rooms[id]
	if !ok {
		return nil, false
	}
	return RoomPermissionsForUser(user, *room), true
}

func (m *RoomManager) List() []Room {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rooms := make([]Room, 0, len(m.order))
	for _, id := range m.order {
		room := m.rooms[id]
		rooms = append(rooms, Room{
			ID:     room.ID,
			Name:   room.Name,
			Grants: cloneRoomGrants(room.Grants),
		})
	}
	return rooms
}

func UserHasRoomPermission(user UserInfo, room Room, permission RoomPermission) bool {
	if user.Role == RoleAdmin {
		return true
	}
	if (user.ID != "" || user.Username != "") && slices.Contains(room.Grants[EveryoneRoomGrant], permission) {
		return true
	}
	for _, group := range user.Groups {
		if slices.Contains(room.Grants[group], permission) {
			return true
		}
	}
	return false
}

func openRoomGrants() map[string][]RoomPermission {
	return map[string][]RoomPermission{
		EveryoneRoomGrant: append([]RoomPermission(nil), roomPermissions...),
	}
}

func RoomPermissionsForUser(user UserInfo, room Room) []RoomPermission {
	permissions := make([]RoomPermission, 0, len(roomPermissions))
	for _, permission := range roomPermissions {
		if UserHasRoomPermission(user, room, permission) {
			permissions = append(permissions, permission)
		}
	}
	return permissions
}

func cloneRoomGrants(grants map[string][]RoomPermission) map[string][]RoomPermission {
	if len(grants) == 0 {
		return nil
	}
	clone := make(map[string][]RoomPermission, len(grants))
	for group, permissions := range grants {
		clone[group] = append([]RoomPermission(nil), permissions...)
	}
	return clone
}
