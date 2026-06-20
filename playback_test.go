package main

import (
	"errors"
	"slices"
	"testing"
)

func TestQueueWaitsForPlayAndSkipAdvances(t *testing.T) {
	p := NewPlayback("default")
	state := p.Add("10", "alice")
	if state.Current.DedupeKey != "" {
		t.Fatalf("current = %q, want nothing playing", state.Current.DedupeKey)
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
	if state.Current.DedupeKey != "10" {
		t.Fatalf("current after play = %q, want 10", state.Current.DedupeKey)
	}
	if state.Current.RequestedBy != "alice" {
		t.Fatalf("current requested by = %q, want alice", state.Current.RequestedBy)
	}
	state = p.Add("20", "alice")
	if len(state.Queue) != 1 {
		t.Fatalf("queue length = %d, want 1", len(state.Queue))
	}
	state = p.Skip()
	if state.Current.DedupeKey != "20" {
		t.Fatalf("current after skip = %q, want 20", state.Current.DedupeKey)
	}
}

func TestSeekUpdatesSharedPosition(t *testing.T) {
	p := NewPlayback("default")
	p.Add("10", "alice")
	if _, err := p.Play(); err != nil {
		t.Fatalf("play: %v", err)
	}

	state := p.SeekTo(30_000)
	if state.Current.DedupeKey != "10" {
		t.Fatalf("current = %q, want 10", state.Current.DedupeKey)
	}
	state = p.Pause()
	if state.PositionAtPauseMS < 30_000 || state.PositionAtPauseMS > 31_000 {
		t.Fatalf("pause position after seek = %d, want about 30000", state.PositionAtPauseMS)
	}
	state = p.SeekTo(12_000)
	if state.PositionAtPauseMS != 12_000 {
		t.Fatalf("pause position = %d, want 12000", state.PositionAtPauseMS)
	}
}

func TestQueueRemoveAndClear(t *testing.T) {
	p := NewPlayback("default")
	p.Add("10", "alice")
	if _, err := p.Play(); err != nil {
		t.Fatalf("play: %v", err)
	}
	state := p.Add("20", "alice")
	if len(state.Queue) != 1 {
		t.Fatalf("queue length = %d, want 1", len(state.Queue))
	}

	state = p.Remove(state.Queue[0].ID)
	if len(state.Queue) != 0 {
		t.Fatalf("queue length after remove = %d, want 0", len(state.Queue))
	}

	p.Add("30", "alice")
	p.Add("40", "alice")
	state = p.Clear()
	if len(state.Queue) != 0 {
		t.Fatalf("queue length after clear = %d, want 0", len(state.Queue))
	}
	if state.Current.DedupeKey != "10" {
		t.Fatalf("current track = %q, want 10", state.Current.DedupeKey)
	}
}

func TestEndedOnlyAdvancesMatchingCurrentTrack(t *testing.T) {
	p := NewPlayback("default")
	p.Add("10", "alice")
	if _, err := p.Play(); err != nil {
		t.Fatalf("play: %v", err)
	}
	p.Add("20", "alice")
	p.Add("30", "alice")

	state := p.Ended("10")
	if state.Current.DedupeKey != "20" {
		t.Fatalf("current after ended = %q, want 20", state.Current.DedupeKey)
	}
	state = p.Ended("10")
	if state.Current.DedupeKey != "20" {
		t.Fatalf("stale ended advanced current to %q, want 20", state.Current.DedupeKey)
	}
}

func TestPreviousPlaysNewestHistoryAndReturnsCurrentToQueue(t *testing.T) {
	p := NewPlayback("default")
	p.Add("10", "alice")
	if _, err := p.Play(); err != nil {
		t.Fatalf("play: %v", err)
	}
	p.Add("20", "alice")
	state := p.Skip()
	if state.Current.DedupeKey != "20" {
		t.Fatalf("current after skip = %q, want 20", state.Current.DedupeKey)
	}
	if len(state.History) != 1 || state.History[0].DedupeKey != "10" {
		t.Fatalf("history = %#v, want track 10", state.History)
	}
	if state.History[0].RequestedBy != "alice" || state.Current.RequestedBy != "alice" {
		t.Fatalf("requesters after skip: current=%q history=%q, want alice/alice", state.Current.RequestedBy, state.History[0].RequestedBy)
	}

	state = p.Previous()
	if state.Current.DedupeKey != "10" {
		t.Fatalf("current after previous = %q, want 10", state.Current.DedupeKey)
	}
	if len(state.History) != 0 {
		t.Fatalf("history length after previous = %d, want 0", len(state.History))
	}
	if len(state.Queue) != 1 || state.Queue[0].DedupeKey != "20" {
		t.Fatalf("queue after previous = %#v, want current track 20 first", state.Queue)
	}
	if state.Current.RequestedBy != "alice" || state.Queue[0].RequestedBy != "alice" {
		t.Fatalf("requesters after previous: current=%q queue=%q, want alice/alice", state.Current.RequestedBy, state.Queue[0].RequestedBy)
	}
}

func TestQueueReorderByQueueItemID(t *testing.T) {
	p := NewPlayback("default")
	p.Add("10", "alice")
	p.Add("20", "alice")
	state := p.Add("30", "alice")
	firstID := state.Queue[0].ID
	secondID := state.Queue[1].ID
	thirdID := state.Queue[2].ID

	state, err := p.Reorder(thirdID, firstID)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{state.Queue[0].DedupeKey, state.Queue[1].DedupeKey, state.Queue[2].DedupeKey}; !slices.Equal(got, []string{"30", "10", "20"}) {
		t.Fatalf("reordered queue = %v, want [30 10 20]", got)
	}

	state, err = p.Reorder(firstID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{state.Queue[0].DedupeKey, state.Queue[1].DedupeKey, state.Queue[2].DedupeKey}; !slices.Equal(got, []string{"30", "20", "10"}) {
		t.Fatalf("queue after move to end = %v, want [30 20 10]", got)
	}

	before := state
	if _, err := p.Reorder(secondID, 999); !errors.Is(err, ErrQueueItemNotFound) {
		t.Fatalf("missing target error = %v, want ErrQueueItemNotFound", err)
	}
	after := p.Snapshot()
	if !slices.EqualFunc(before.Queue, after.Queue, func(a, b PlaybackItem) bool { return a.ID == b.ID }) {
		t.Fatalf("failed reorder changed queue: before=%v after=%v", before.Queue, after.Queue)
	}
}

func TestPlayNowStartsTrackAndRecordsHistory(t *testing.T) {
	p := NewPlayback("default")
	p.Add("10", "alice")
	if _, err := p.Play(); err != nil {
		t.Fatalf("play: %v", err)
	}
	p.Add("20", "alice")

	state := p.PlayNow("20", "bob")
	if state.Current.DedupeKey != "20" {
		t.Fatalf("current = %q, want 20", state.Current.DedupeKey)
	}
	if state.Current.RequestedBy != "bob" {
		t.Fatalf("current requested by = %q, want bob", state.Current.RequestedBy)
	}
	if len(state.Queue) != 0 {
		t.Fatalf("queue length = %d, want 0", len(state.Queue))
	}
	if len(state.History) != 1 || state.History[0].DedupeKey != "10" {
		t.Fatalf("history = %#v, want previous track 10", state.History)
	}
	if state.History[0].RequestedBy != "alice" {
		t.Fatalf("history requested by = %q, want alice", state.History[0].RequestedBy)
	}
}

func TestClearHistory(t *testing.T) {
	p := NewPlayback("default")
	p.Add("10", "alice")
	if _, err := p.Play(); err != nil {
		t.Fatalf("play: %v", err)
	}
	state := p.PlayNow("20", "bob")
	if len(state.History) != 1 {
		t.Fatalf("history length = %d, want 1", len(state.History))
	}

	state = p.ClearHistory()
	if len(state.History) != 0 {
		t.Fatalf("history length after clear = %d, want 0", len(state.History))
	}
	if state.Current.DedupeKey != "20" {
		t.Fatalf("current after clear = %q, want 20", state.Current.DedupeKey)
	}
}

func TestSubscribeUpdatesListenerCount(t *testing.T) {
	p := NewPlayback("default")
	ch, cancel := p.Subscribe(UserInfo{ID: "user1", Username: "alice"})
	state := <-ch
	if len(state.Listeners) != 1 {
		t.Fatalf("listener count = %d, want 1", len(state.Listeners))
	}
	if len(state.Listeners) != 1 || state.Listeners[0] != "alice" {
		t.Fatalf("listeners = %v, want [alice]", state.Listeners)
	}
	cancel()
	state = p.Snapshot()
	if len(state.Listeners) != 0 {
		t.Fatalf("listener count after cancel = %d, want 0", len(state.Listeners))
	}
}

func TestSubscribeCountsDistinctListenerUsers(t *testing.T) {
	p := NewPlayback("default")
	_, cancelA := p.Subscribe(UserInfo{ID: "user1", Username: "alice"})
	defer cancelA()
	_, cancelB := p.Subscribe(UserInfo{ID: "user1", Username: "alice"})
	defer cancelB()
	_, cancelC := p.Subscribe(UserInfo{ID: "user2", Username: "bob"})
	defer cancelC()

	state := p.Snapshot()
	if len(state.Listeners) != 2 {
		t.Fatalf("listener count = %d, want 2", len(state.Listeners))
	}
	if len(state.Listeners) != 2 || state.Listeners[0] != "alice" || state.Listeners[1] != "bob" {
		t.Fatalf("listeners = %v, want [alice bob]", state.Listeners)
	}
}

func TestCloseSubscribersClosesActiveSubscriptions(t *testing.T) {
	p := NewPlayback("default")
	ch, cancel := p.Subscribe(UserInfo{ID: "user1", Username: "alice"})
	defer cancel()
	<-ch

	p.CloseSubscribers()

	if _, ok := <-ch; ok {
		t.Fatal("subscription channel remained open")
	}
	state := p.Snapshot()
	if len(state.Listeners) != 0 {
		t.Fatalf("listener count = %d, want 0", len(state.Listeners))
	}
}
