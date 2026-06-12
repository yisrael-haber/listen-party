package main

import "testing"

func TestQueueWaitsForPlayAndSkipAdvances(t *testing.T) {
	p := NewPlayback("default")
	state := p.Add("default", 10)
	if state.CurrentTrackID != 0 {
		t.Fatalf("current = %d, want nothing playing", state.CurrentTrackID)
	}
	if len(state.Queue) != 1 {
		t.Fatalf("queue length = %d, want 1", len(state.Queue))
	}
	state, err := p.Play("default")
	if err != nil {
		t.Fatalf("play: %v", err)
	}
	if state.CurrentTrackID != 10 {
		t.Fatalf("current after play = %d, want 10", state.CurrentTrackID)
	}
	state = p.Add("default", 20)
	if len(state.Queue) != 1 {
		t.Fatalf("queue length = %d, want 1", len(state.Queue))
	}
	state = p.Skip("default")
	if state.CurrentTrackID != 20 {
		t.Fatalf("current after skip = %d, want 20", state.CurrentTrackID)
	}
}

func TestSeekUpdatesSharedPosition(t *testing.T) {
	p := NewPlayback("default")
	p.Add("default", 10)
	if _, err := p.Play("default"); err != nil {
		t.Fatalf("play: %v", err)
	}

	state := p.Seek("default", 30_000)
	if state.CurrentTrackID != 10 {
		t.Fatalf("current = %d, want 10", state.CurrentTrackID)
	}
	if state.StartedAt.IsZero() {
		t.Fatal("started_at should be set")
	}

	state = p.Pause("default")
	state = p.Seek("default", 12_000)
	if state.PositionAtPauseMS != 12_000 {
		t.Fatalf("pause position = %d, want 12000", state.PositionAtPauseMS)
	}
}

func TestQueueRemoveAndClear(t *testing.T) {
	p := NewPlayback("default")
	p.Add("default", 10)
	if _, err := p.Play("default"); err != nil {
		t.Fatalf("play: %v", err)
	}
	state := p.Add("default", 20)
	if len(state.Queue) != 1 {
		t.Fatalf("queue length = %d, want 1", len(state.Queue))
	}

	state = p.Remove("default", state.Queue[0].ID)
	if len(state.Queue) != 0 {
		t.Fatalf("queue length after remove = %d, want 0", len(state.Queue))
	}

	p.Add("default", 30)
	p.Add("default", 40)
	state = p.Clear("default")
	if len(state.Queue) != 0 {
		t.Fatalf("queue length after clear = %d, want 0", len(state.Queue))
	}
	if state.CurrentTrackID != 10 {
		t.Fatalf("current track = %d, want 10", state.CurrentTrackID)
	}
}

func TestEndedOnlyAdvancesMatchingCurrentTrack(t *testing.T) {
	p := NewPlayback("default")
	p.Add("default", 10)
	if _, err := p.Play("default"); err != nil {
		t.Fatalf("play: %v", err)
	}
	p.Add("default", 20)
	p.Add("default", 30)

	state := p.Ended("default", 10)
	if state.CurrentTrackID != 20 {
		t.Fatalf("current after ended = %d, want 20", state.CurrentTrackID)
	}
	state = p.Ended("default", 10)
	if state.CurrentTrackID != 20 {
		t.Fatalf("stale ended advanced current to %d, want 20", state.CurrentTrackID)
	}
}

func TestPreviousPlaysNewestHistoryAndReturnsCurrentToQueue(t *testing.T) {
	p := NewPlayback("default")
	p.Add("default", 10)
	if _, err := p.Play("default"); err != nil {
		t.Fatalf("play: %v", err)
	}
	p.Add("default", 20)
	state := p.Skip("default")
	if state.CurrentTrackID != 20 {
		t.Fatalf("current after skip = %d, want 20", state.CurrentTrackID)
	}
	if len(state.History) != 1 || state.History[0].TrackID != 10 {
		t.Fatalf("history = %#v, want track 10", state.History)
	}

	state = p.Previous("default")
	if state.CurrentTrackID != 10 {
		t.Fatalf("current after previous = %d, want 10", state.CurrentTrackID)
	}
	if len(state.History) != 0 {
		t.Fatalf("history length after previous = %d, want 0", len(state.History))
	}
	if len(state.Queue) != 1 || state.Queue[0].TrackID != 20 {
		t.Fatalf("queue after previous = %#v, want current track 20 first", state.Queue)
	}
}

func TestPlaybackIDChangesForEachStartedTrack(t *testing.T) {
	p := NewPlayback("default")
	p.Add("default", 10)
	first, err := p.Play("default")
	if err != nil {
		t.Fatalf("play: %v", err)
	}
	p.Add("default", 10)
	second := p.Skip("default")

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
	p.Add("default", 10)
	p.Add("default", 20)
	state := p.Add("default", 30)

	state = p.Move("default", state.Queue[2].ID, -1)
	if got := state.Queue[1].TrackID; got != 30 {
		t.Fatalf("moved queue item = %d, want 30", got)
	}
	state = p.MoveToNext("default", state.Queue[1].ID)
	if got := state.Queue[0].TrackID; got != 30 {
		t.Fatalf("next queue item = %d, want 30", got)
	}
}

func TestPlayNowStartsTrackAndRecordsHistory(t *testing.T) {
	p := NewPlayback("default")
	p.Add("default", 10)
	if _, err := p.Play("default"); err != nil {
		t.Fatalf("play: %v", err)
	}
	p.Add("default", 20)

	state := p.PlayNow("default", 20)
	if state.CurrentTrackID != 20 {
		t.Fatalf("current = %d, want 20", state.CurrentTrackID)
	}
	if len(state.Queue) != 0 {
		t.Fatalf("queue length = %d, want 0", len(state.Queue))
	}
	if len(state.History) != 1 || state.History[0].TrackID != 10 {
		t.Fatalf("history = %#v, want previous track 10", state.History)
	}
}

func TestClearHistory(t *testing.T) {
	p := NewPlayback("default")
	p.Add("default", 10)
	if _, err := p.Play("default"); err != nil {
		t.Fatalf("play: %v", err)
	}
	state := p.PlayNow("default", 20)
	if len(state.History) != 1 {
		t.Fatalf("history length = %d, want 1", len(state.History))
	}

	state = p.ClearHistory("default")
	if len(state.History) != 0 {
		t.Fatalf("history length after clear = %d, want 0", len(state.History))
	}
	if state.CurrentTrackID != 20 {
		t.Fatalf("current after clear = %d, want 20", state.CurrentTrackID)
	}
}

func TestSubscribeUpdatesListenerCount(t *testing.T) {
	p := NewPlayback("default")
	ch, cancel := p.Subscribe("default")
	state := <-ch
	if state.ListenerCount != 1 {
		t.Fatalf("listener count = %d, want 1", state.ListenerCount)
	}
	cancel()
	state = p.Snapshot("default")
	if state.ListenerCount != 0 {
		t.Fatalf("listener count after cancel = %d, want 0", state.ListenerCount)
	}
}
