package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	musiclib "listen-party/internal/library"
)

func (s *Server) savePlayback(ctx context.Context, room *Room) error {
	if s.Library == nil || room == nil {
		return nil
	}
	state := room.Playback.PersistentState()
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal room playback state: %w", err)
	}
	return s.Library.SaveRoomPlaybackSnapshot(ctx, musiclib.RoomPlaybackSnapshot{
		RoomID:   room.ID,
		Revision: state.Revision,
		State:    data,
	})
}

func (s *Server) restorePlayback(ctx context.Context) error {
	if s.Library == nil || s.Rooms == nil {
		return nil
	}
	snapshots, err := s.Library.LoadRoomPlaybackSnapshots(ctx)
	if err != nil {
		return fmt.Errorf("load room playback snapshots: %w", err)
	}
	for _, snapshot := range snapshots {
		room, ok := s.Rooms.Get(snapshot.RoomID)
		if !ok {
			continue
		}
		var state persistedPlayback
		if err := json.Unmarshal(snapshot.State, &state); err != nil {
			slog.Warn("ignore corrupt room playback snapshot", "room", snapshot.RoomID, "error", err)
			continue
		}
		room.Playback.RestorePersistentState(state, snapshot.Revision)
		current := s.stabilizeAndSchedulePlayback(ctx, room, room.Playback.Snapshot())
		if err := s.savePlayback(ctx, room); err != nil {
			return fmt.Errorf("save restored room %q: %w", room.ID, err)
		}
		slog.Info("restored room playback", "room", room.ID, "revision", current.Revision)
	}
	return nil
}
