package main

import (
	"testing"
	"time"
)

func TestRoomManagerPreservesPlaybackForUnchangedRooms(t *testing.T) {
	rooms := NewRoomManager([]Room{
		{ID: "main", Name: "Main Room"},
		{ID: "office", Name: "Office"},
	})
	office, ok := rooms.Get("office")
	if !ok {
		t.Fatal("office room missing")
	}
	office.Playback.Add("track-10", "alice")

	rooms.Update([]Room{
		{ID: "main", Name: "Main Room"},
		{ID: "office", Name: "Office Renamed"},
	})
	office, ok = rooms.Get("office")
	if !ok {
		t.Fatal("office room missing after update")
	}
	if got := len(office.Playback.Snapshot().Queue); got != 1 {
		t.Fatalf("office queue length = %d, want 1", got)
	}
	if office.Name != "Office Renamed" {
		t.Fatalf("office name = %q, want Office Renamed", office.Name)
	}
}

func TestRoomManagerClosesRemovedRoomSubscribers(t *testing.T) {
	rooms := NewRoomManager([]Room{
		{ID: "main", Name: "Main Room"},
		{ID: "office", Name: "Office"},
	})
	office, ok := rooms.Get("office")
	if !ok {
		t.Fatal("office room missing")
	}
	ch, cancel := office.Playback.Subscribe(UserInfo{ID: "user1", Username: "alice"})
	defer cancel()
	<-ch

	rooms.Update([]Room{{ID: "main", Name: "Main Room"}})

	if _, ok := <-ch; ok {
		t.Fatal("removed room subscription remained open")
	}
}

func TestUserHasRoomPermission(t *testing.T) {
	room := Room{ID: "office", Name: "Office", Grants: map[string][]RoomPermission{
		"staff": {PermissionQueueManage},
	}}
	if UserHasRoomPermission(UserInfo{}, room, PermissionQueueManage) {
		t.Fatal("user without a group received queue permission")
	}
	if !UserHasRoomPermission(UserInfo{Groups: []string{"staff"}}, room, PermissionQueueManage) {
		t.Fatal("staff user did not receive queue permission")
	}
	if UserHasRoomPermission(UserInfo{Groups: []string{"staff"}}, room, PermissionPlaybackControl) {
		t.Fatal("queue permission implied playback permission")
	}
	if UserHasRoomPermission(UserInfo{Groups: []string{"staff"}}, room, PermissionQueueAdd) {
		t.Fatal("queue management implied queue addition")
	}
	if !UserHasRoomPermission(UserInfo{Role: RoleAdmin}, room, PermissionPlaybackControl) {
		t.Fatal("admin did not receive implicit permissions")
	}
	if !UserHasRoomPermission(UserInfo{Role: RoleAdmin}, room, PermissionQueueAdd) {
		t.Fatal("admin did not receive implicit queue addition")
	}
}

func TestEveryoneRoomGrantAppliesOnlyToAuthenticatedUsers(t *testing.T) {
	room := Room{ID: "main", Name: "Public Room", Grants: openRoomGrants()}
	if UserHasRoomPermission(UserInfo{}, room, PermissionQueueAdd) {
		t.Fatal("anonymous identity received everyone permission")
	}
	user := UserInfo{ID: "user1", Username: "alice"}
	for _, permission := range roomPermissions {
		if !UserHasRoomPermission(user, room, permission) {
			t.Fatalf("enabled user missing everyone permission %q", permission)
		}
	}
}

func TestRoomPermissionUpdatesApplyImmediately(t *testing.T) {
	user := UserInfo{Groups: []string{"staff"}}
	rooms := NewRoomManager([]Room{{
		ID: "main", Name: "Main Room",
		Grants: map[string][]RoomPermission{"staff": {PermissionQueueManage}},
	}})
	room, _ := rooms.Get("main")
	if !UserHasRoomPermission(user, *room, PermissionQueueManage) {
		t.Fatal("initial queue permission missing")
	}

	rooms.Update([]Room{{ID: "main", Name: "Main Room"}})
	room, _ = rooms.Get("main")
	if UserHasRoomPermission(user, *room, PermissionQueueManage) {
		t.Fatal("removed queue permission remained effective")
	}
}

func TestRoomUpdateNotifiesConnectedListeners(t *testing.T) {
	rooms := NewRoomManager([]Room{{ID: "main", Name: "Main Room"}})
	room, _ := rooms.Get("main")
	updates, cancel := room.Playback.Subscribe(UserInfo{Username: "alice"})
	defer cancel()
	<-updates

	rooms.Update([]Room{{
		ID: "main", Name: "Main Room",
		Grants: map[string][]RoomPermission{"staff": {PermissionQueueManage}},
	}})
	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("room update did not notify connected listener")
	}
}
