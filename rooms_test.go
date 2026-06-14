package main

import "testing"

func TestRoomManagerPreservesPlaybackForUnchangedRooms(t *testing.T) {
	rooms := NewRoomManager([]RoomConfig{
		{ID: "public", Name: "Public Room", Public: true},
		{ID: "office", Name: "Office"},
	})
	office, ok := rooms.Get("office")
	if !ok {
		t.Fatal("office room missing")
	}
	office.Playback.Add(10, "alice")

	rooms.Update([]RoomConfig{
		{ID: "public", Name: "Public Room", Public: true},
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
	rooms := NewRoomManager([]RoomConfig{
		{ID: "public", Name: "Public Room", Public: true},
		{ID: "office", Name: "Office"},
	})
	office, ok := rooms.Get("office")
	if !ok {
		t.Fatal("office room missing")
	}
	ch, cancel := office.Playback.Subscribe(ActiveListener{UserID: "user1", Username: "alice"})
	defer cancel()
	<-ch

	rooms.Update([]RoomConfig{{ID: "public", Name: "Public Room", Public: true}})

	if _, ok := <-ch; ok {
		t.Fatal("removed room subscription remained open")
	}
}

func TestUserCanAccessRoom(t *testing.T) {
	private := Room{ID: "office", Name: "Office", AllowedGroups: []string{"staff"}}
	if UserCanAccessRoom(UserInfo{Role: RoleListener}, private) {
		t.Fatal("listener without room or group accessed private room")
	}
	if !UserCanAccessRoom(UserInfo{Role: RoleListener, RoomIDs: []string{"office"}}, private) {
		t.Fatal("listener with room id could not access private room")
	}
	if !UserCanAccessRoom(UserInfo{Role: RoleListener, Groups: []string{"staff"}}, private) {
		t.Fatal("listener with group could not access private room")
	}
	if !UserCanAccessRoom(UserInfo{Role: RoleAdmin}, private) {
		t.Fatal("admin could not access private room")
	}
	if !UserCanAccessRoom(UserInfo{Role: RoleListener}, Room{ID: "public", Name: "Public", Public: true}) {
		t.Fatal("listener could not access public room")
	}
}
