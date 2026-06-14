package main

import "testing"

func TestQueueWaitsForPlayAndSkipAdvances(t *testing.T) {
	p := NewPlayback("default")
	state := p.Add(10, "alice")
	if state.CurrentTrackID != 0 {
		t.Fatalf("current = %d, want nothing playing", state.CurrentTrackID)
	}
	if len(state.Queue) != 1 {
		t.Fatalf("queue length = %d, want 1", len(state.Queue))
	}
	if state.Queue[0].RequestedBy != "alice" {
		t.Fatalf("queued by = %q, want alice", state.Queue[0].RequestedBy)
	}
	state, err := p.Play()
	if err != nil {
		t.Fatalf("play: %v", err)
	}
	if state.CurrentTrackID != 10 {
		t.Fatalf("current after play = %d, want 10", state.CurrentTrackID)
	}
	if state.CurrentRequestedBy != "alice" {
		t.Fatalf("current requested by = %q, want alice", state.CurrentRequestedBy)
	}
	state = p.Add(20, "alice")
	if len(state.Queue) != 1 {
		t.Fatalf("queue length = %d, want 1", len(state.Queue))
	}
	state = p.Skip()
	if state.CurrentTrackID != 20 {
		t.Fatalf("current after skip = %d, want 20", state.CurrentTrackID)
	}
}

func TestSeekUpdatesSharedPosition(t *testing.T) {
	p := NewPlayback("default")
	p.Add(10, "alice")
	if _, err := p.Play(); err != nil {
		t.Fatalf("play: %v", err)
	}

	state := p.Seek(30_000)
	if state.CurrentTrackID != 10 {
		t.Fatalf("current = %d, want 10", state.CurrentTrackID)
	}
	if state.StartedAt.IsZero() {
		t.Fatal("started_at should be set")
	}

	state = p.Pause()
	state = p.Seek(12_000)
	if state.PositionAtPauseMS != 12_000 {
		t.Fatalf("pause position = %d, want 12000", state.PositionAtPauseMS)
	}
}

func TestQueueRemoveAndClear(t *testing.T) {
	p := NewPlayback("default")
	p.Add(10, "alice")
	if _, err := p.Play(); err != nil {
		t.Fatalf("play: %v", err)
	}
	state := p.Add(20, "alice")
	if len(state.Queue) != 1 {
		t.Fatalf("queue length = %d, want 1", len(state.Queue))
	}

	state = p.Remove(state.Queue[0].ID)
	if len(state.Queue) != 0 {
		t.Fatalf("queue length after remove = %d, want 0", len(state.Queue))
	}

	p.Add(30, "alice")
	p.Add(40, "alice")
	state = p.Clear()
	if len(state.Queue) != 0 {
		t.Fatalf("queue length after clear = %d, want 0", len(state.Queue))
	}
	if state.CurrentTrackID != 10 {
		t.Fatalf("current track = %d, want 10", state.CurrentTrackID)
	}
}

func TestEndedOnlyAdvancesMatchingCurrentTrack(t *testing.T) {
	p := NewPlayback("default")
	p.Add(10, "alice")
	if _, err := p.Play(); err != nil {
		t.Fatalf("play: %v", err)
	}
	p.Add(20, "alice")
	p.Add(30, "alice")

	state := p.Ended(10)
	if state.CurrentTrackID != 20 {
		t.Fatalf("current after ended = %d, want 20", state.CurrentTrackID)
	}
	state = p.Ended(10)
	if state.CurrentTrackID != 20 {
		t.Fatalf("stale ended advanced current to %d, want 20", state.CurrentTrackID)
	}
}

func TestPreviousPlaysNewestHistoryAndReturnsCurrentToQueue(t *testing.T) {
	p := NewPlayback("default")
	p.Add(10, "alice")
	if _, err := p.Play(); err != nil {
		t.Fatalf("play: %v", err)
	}
	p.Add(20, "alice")
	state := p.Skip()
	if state.CurrentTrackID != 20 {
		t.Fatalf("current after skip = %d, want 20", state.CurrentTrackID)
	}
	if len(state.History) != 1 || state.History[0].TrackID != 10 {
		t.Fatalf("history = %#v, want track 10", state.History)
	}
	if state.History[0].RequestedBy != "alice" || state.CurrentRequestedBy != "alice" {
		t.Fatalf("requesters after skip: current=%q history=%q, want alice/alice", state.CurrentRequestedBy, state.History[0].RequestedBy)
	}

	state = p.Previous()
	if state.CurrentTrackID != 10 {
		t.Fatalf("current after previous = %d, want 10", state.CurrentTrackID)
	}
	if len(state.History) != 0 {
		t.Fatalf("history length after previous = %d, want 0", len(state.History))
	}
	if len(state.Queue) != 1 || state.Queue[0].TrackID != 20 {
		t.Fatalf("queue after previous = %#v, want current track 20 first", state.Queue)
	}
	if state.CurrentRequestedBy != "alice" || state.Queue[0].RequestedBy != "alice" {
		t.Fatalf("requesters after previous: current=%q queue=%q, want alice/alice", state.CurrentRequestedBy, state.Queue[0].RequestedBy)
	}
}

func TestPlaybackIDChangesForEachStartedTrack(t *testing.T) {
	p := NewPlayback("default")
	p.Add(10, "alice")
	first, err := p.Play()
	if err != nil {
		t.Fatalf("play: %v", err)
	}
	p.Add(10, "alice")
	second := p.Skip()

	if first.PlaybackID == 0 {
		t.Fatal("first playback id should be set")
	}
	if second.PlaybackID <= first.PlaybackID {
		t.Fatalf("second playback id = %d, want greater than %d", second.PlaybackID, first.PlaybackID)
	}
	if second.CurrentTrackID != 10 {
		t.Fatalf("current = %d, want same track id 10", second.CurrentTrackID)
	}
}

func TestQueueMoveAndMoveToNext(t *testing.T) {
	p := NewPlayback("default")
	p.Add(10, "alice")
	p.Add(20, "alice")
	state := p.Add(30, "alice")

	state = p.Move(state.Queue[2].ID, -1)
	if got := state.Queue[1].TrackID; got != 30 {
		t.Fatalf("moved queue item = %d, want 30", got)
	}
	state = p.MoveToNext(state.Queue[1].ID)
	if got := state.Queue[0].TrackID; got != 30 {
		t.Fatalf("next queue item = %d, want 30", got)
	}
}

func TestPlayNowStartsTrackAndRecordsHistory(t *testing.T) {
	p := NewPlayback("default")
	p.Add(10, "alice")
	if _, err := p.Play(); err != nil {
		t.Fatalf("play: %v", err)
	}
	p.Add(20, "alice")

	state := p.PlayNow(20, "bob")
	if state.CurrentTrackID != 20 {
		t.Fatalf("current = %d, want 20", state.CurrentTrackID)
	}
	if state.CurrentRequestedBy != "bob" {
		t.Fatalf("current requested by = %q, want bob", state.CurrentRequestedBy)
	}
	if len(state.Queue) != 0 {
		t.Fatalf("queue length = %d, want 0", len(state.Queue))
	}
	if len(state.History) != 1 || state.History[0].TrackID != 10 {
		t.Fatalf("history = %#v, want previous track 10", state.History)
	}
	if state.History[0].RequestedBy != "alice" {
		t.Fatalf("history requested by = %q, want alice", state.History[0].RequestedBy)
	}
}

func TestClearHistory(t *testing.T) {
	p := NewPlayback("default")
	p.Add(10, "alice")
	if _, err := p.Play(); err != nil {
		t.Fatalf("play: %v", err)
	}
	state := p.PlayNow(20, "bob")
	if len(state.History) != 1 {
		t.Fatalf("history length = %d, want 1", len(state.History))
	}

	state = p.ClearHistory()
	if len(state.History) != 0 {
		t.Fatalf("history length after clear = %d, want 0", len(state.History))
	}
	if state.CurrentTrackID != 20 {
		t.Fatalf("current after clear = %d, want 20", state.CurrentTrackID)
	}
}

func TestSubscribeUpdatesListenerCount(t *testing.T) {
	p := NewPlayback("default")
	ch, cancel := p.Subscribe(ActiveListener{UserID: "user1", Username: "alice"})
	state := <-ch
	if state.ListenerCount != 1 {
		t.Fatalf("listener count = %d, want 1", state.ListenerCount)
	}
	if len(state.Listeners) != 1 || state.Listeners[0] != "alice" {
		t.Fatalf("listeners = %v, want [alice]", state.Listeners)
	}
	cancel()
	state = p.Snapshot()
	if state.ListenerCount != 0 {
		t.Fatalf("listener count after cancel = %d, want 0", state.ListenerCount)
	}
}

func TestSubscribeCountsDistinctListenerUsers(t *testing.T) {
	p := NewPlayback("default")
	_, cancelA := p.Subscribe(ActiveListener{UserID: "user1", Username: "alice"})
	defer cancelA()
	_, cancelB := p.Subscribe(ActiveListener{UserID: "user1", Username: "alice"})
	defer cancelB()
	_, cancelC := p.Subscribe(ActiveListener{UserID: "user2", Username: "bob"})
	defer cancelC()

	state := p.Snapshot()
	if state.ListenerCount != 2 {
		t.Fatalf("listener count = %d, want 2", state.ListenerCount)
	}
	if len(state.Listeners) != 2 || state.Listeners[0] != "alice" || state.Listeners[1] != "bob" {
		t.Fatalf("listeners = %v, want [alice bob]", state.Listeners)
	}
}

func TestCloseSubscribersClosesActiveSubscriptions(t *testing.T) {
	p := NewPlayback("default")
	ch, cancel := p.Subscribe(ActiveListener{UserID: "user1", Username: "alice"})
	defer cancel()
	<-ch

	p.CloseSubscribers()

	if _, ok := <-ch; ok {
		t.Fatal("subscription channel remained open")
	}
	state := p.Snapshot()
	if state.ListenerCount != 0 {
		t.Fatalf("listener count = %d, want 0", state.ListenerCount)
	}
}
