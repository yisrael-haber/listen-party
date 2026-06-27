package main

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestQueueWaitsForPlayAndSkipAdvances(t *testing.T) {
	p := NewPlayback("default")
	state, _ := p.Add("10", "alice")
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
	state, _ = p.Add("20", "alice")
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

func TestRoomAudioIsSharedPlaybackState(t *testing.T) {
	p := NewPlayback("default")
	if got := p.Snapshot().RoomAudio; got != (RoomAudio{Volume: 0.25}) {
		t.Fatalf("default room audio = %#v", got)
	}
	state := p.SetRoomAudio(0.4, true)
	if state.RoomAudio != (RoomAudio{Volume: 0.4, Muted: true}) {
		t.Fatalf("room audio = %#v", state.RoomAudio)
	}
}

func TestRoomActionsKeepLastTwenty(t *testing.T) {
	p := NewPlayback("default")
	for i := range 25 {
		p.AddAction(RoomAction{Username: "alice", Text: string(rune('a' + i))})
	}
	actions := p.Snapshot().Actions
	if len(actions) != 20 {
		t.Fatalf("actions length = %d, want 20", len(actions))
	}
	if actions[0].Text != "y" || actions[19].Text != "f" {
		t.Fatalf("actions = %#v, want newest 20 first", actions)
	}
}

func TestQueueRemoveAndClear(t *testing.T) {
	p := NewPlayback("default")
	p.Add("10", "alice")
	if _, err := p.Play(); err != nil {
		t.Fatalf("play: %v", err)
	}
	state, _ := p.Add("20", "alice")
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

func TestQueueLimitRejectsWithoutChangingState(t *testing.T) {
	p := NewPlayback("default")
	for i := range maxQueueItems {
		if _, err := p.Add(fmt.Sprintf("track-%d", i), "alice"); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	before := p.Snapshot()
	after, err := p.Add("overflow", "alice")
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("overflow error = %v, want ErrQueueFull", err)
	}
	if len(after.Queue) != maxQueueItems || after.Revision != before.Revision {
		t.Fatalf("rejected add changed state: before=%#v after=%#v", before, after)
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
	state, _ := p.Add("30", "alice")
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
	p.listenerGrace = 0
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

func TestSubscriberReceivesLatestState(t *testing.T) {
	p := NewPlayback("default")
	ch, cancel := p.Subscribe(UserInfo{ID: "user1", Username: "alice"})
	defer cancel()
	<-ch
	for _, key := range []string{"one", "two", "three"} {
		if _, err := p.Add(key, "alice"); err != nil {
			t.Fatal(err)
		}
	}
	state := <-ch
	if len(state.Queue) != 3 || state.Queue[2].DedupeKey != "three" {
		t.Fatalf("subscriber state = %#v, want latest queue", state)
	}
	select {
	case stale := <-ch:
		t.Fatalf("subscriber retained stale state: %#v", stale)
	default:
	}
}

func TestListenerPresenceSurvivesTransportReconnect(t *testing.T) {
	p := NewPlayback("default")
	p.listenerGrace = 20 * time.Millisecond

	_, cancel := p.Subscribe(UserInfo{ID: "user1", Username: "alice"})
	cancel()
	if listeners := p.Snapshot().Listeners; len(listeners) != 1 || listeners[0] != "alice" {
		t.Fatalf("listeners during reconnect grace = %v, want [alice]", listeners)
	}

	_, reconnectCancel := p.Subscribe(UserInfo{ID: "user1", Username: "alice"})
	time.Sleep(30 * time.Millisecond)
	if listeners := p.Snapshot().Listeners; len(listeners) != 1 || listeners[0] != "alice" {
		t.Fatalf("listeners after reconnect = %v, want [alice]", listeners)
	}
	reconnectCancel()

	deadline := time.Now().Add(200 * time.Millisecond)
	for len(p.Snapshot().Listeners) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if listeners := p.Snapshot().Listeners; len(listeners) != 0 {
		t.Fatalf("listeners after grace = %v, want none", listeners)
	}
}

func TestListenerNamesAreDeduplicatedAcrossIdentityRepresentations(t *testing.T) {
	p := NewPlayback("default")
	_, cancelA := p.Subscribe(UserInfo{ID: "user1", Username: "alice"})
	defer cancelA()
	_, cancelB := p.Subscribe(UserInfo{Username: "Alice"})
	defer cancelB()

	if listeners := p.Snapshot().Listeners; len(listeners) != 1 || listeners[0] != "alice" {
		t.Fatalf("listeners = %v, want one alice", listeners)
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

func TestDisconnectListenerBlocksOnlyTheActiveSession(t *testing.T) {
	p := NewPlayback("main")
	listener := UserInfo{ID: "user1", Username: "alice", SessionKey: "session:old"}
	ch, cancel, allowed := p.SubscribeIfAllowed(listener)
	defer cancel()
	if !allowed {
		t.Fatal("initial listener was rejected")
	}
	<-ch
	if !p.DisconnectListener("alice") {
		t.Fatal("active listener was not disconnected")
	}
	state, ok := <-ch
	if !ok || !state.Disconnect {
		t.Fatalf("disconnect event = %#v, open = %v", state, ok)
	}
	if _, ok := <-ch; ok {
		t.Fatal("disconnected subscription remained open")
	}
	if _, _, allowed := p.SubscribeIfAllowed(listener); allowed {
		t.Fatal("disconnected session was allowed to reconnect automatically")
	}
	newSession := listener
	newSession.SessionKey = "session:new"
	newCh, newCancel, allowed := p.SubscribeIfAllowed(newSession)
	defer newCancel()
	if !allowed {
		t.Fatal("fresh login session was rejected")
	}
	<-newCh
}

func TestAutoDJStartsPreparedTrackOnlyAfterQueueIsExhausted(t *testing.T) {
	p := NewPlayback("main")
	p.ConfigureAutoDJ(true, "random", nil)
	p.Add("queued", "alice")
	state, err := p.Play()
	if err != nil {
		t.Fatal(err)
	}
	if state.Current.DedupeKey != "queued" || state.Current.Source != "user" {
		t.Fatalf("first current = %#v, want queued user track", state.Current)
	}
	state = p.Skip()
	if state.Current.DedupeKey != "random" || state.Current.Source != "auto_dj" || state.Current.RequestedBy != "" {
		t.Fatalf("auto-dj current = %#v", state.Current)
	}
	if _, candidate := p.AutoDJConfiguration(); candidate != "" {
		t.Fatalf("consumed candidate = %q, want empty", candidate)
	}
}

func TestDisablingAutoDJClearsPreparedTrack(t *testing.T) {
	p := NewPlayback("main")
	p.ConfigureAutoDJ(true, "random", []int64{1, 2})
	state := p.ConfigureAutoDJ(false, "", nil)
	if state.AutoDJ.Enabled {
		t.Fatal("auto-dj remained enabled")
	}
	if _, err := p.Play(); !errors.Is(err, ErrEmptyQueue) {
		t.Fatalf("play error = %v, want ErrEmptyQueue", err)
	}
}

func TestAutoDJSourceCanChangeWithoutEnablingPlayback(t *testing.T) {
	p := NewPlayback("main")
	source := AutoDJSource{Type: AutoDJSourcePlaylist, PlaylistID: 7, Name: "Evening"}
	state := p.ConfigureAutoDJSource(source, "prepared", nil)
	if state.AutoDJ.Enabled || state.AutoDJ.Source != source {
		t.Fatalf("auto-dj = %#v, want disabled playlist source", state.AutoDJ)
	}
	if _, candidate := p.AutoDJConfiguration(); candidate != "" {
		t.Fatalf("disabled candidate = %q, want empty", candidate)
	}
	state = p.ConfigureAutoDJ(true, "prepared", nil)
	if !state.AutoDJ.Enabled || state.AutoDJ.Source != source {
		t.Fatalf("enabled auto-dj = %#v, want playlist source", state.AutoDJ)
	}
}

func TestPlaylistChangesInvalidateAutoDJCandidates(t *testing.T) {
	p := NewPlayback("main")
	source := AutoDJSource{Type: AutoDJSourcePlaylist, PlaylistID: 7, Name: "Evening"}
	p.ConfigureAutoDJSource(source, "", nil)
	p.ConfigureAutoDJ(true, "prepared", []int64{1, 2})
	p.InvalidateAutoDJPlaylistCandidate(source.PlaylistID)
	if _, candidate := p.AutoDJConfiguration(); candidate != "" {
		t.Fatalf("candidate after playlist edit = %q, want empty", candidate)
	}
	if !p.ResetAutoDJPlaylistSource(source.PlaylistID) {
		t.Fatal("playlist source was not reset")
	}
	state := p.Snapshot().AutoDJ
	if state.Enabled || state.Source != defaultAutoDJSource() {
		t.Fatalf("auto-dj after playlist deletion = %#v", state)
	}
}

func TestAutoDJEntriesAreConsumedFromTheShuffledBag(t *testing.T) {
	p := NewPlayback("main")
	source := defaultAutoDJSource()
	p.ConfigureAutoDJ(true, "current", []int64{1, 2})
	p.ClearAutoDJCandidate(source)
	if !p.BeginAutoDJCandidate(source) {
		t.Fatal("could not begin candidate preparation")
	}
	for _, want := range []int64{2, 1} {
		got, ok := p.TakeAutoDJEntry(source)
		if !ok || got != want {
			t.Fatalf("entry = %d, %v, want %d, true", got, ok, want)
		}
	}
	if _, ok := p.TakeAutoDJEntry(source); ok {
		t.Fatal("exhausted shuffle bag returned another entry")
	}
}

func TestQueuedTrackConsumesMatchingAutoDJCandidate(t *testing.T) {
	p := NewPlayback("main")
	p.ConfigureAutoDJ(true, "same", nil)
	p.Add("same", "alice")
	if _, err := p.Play(); err != nil {
		t.Fatal(err)
	}
	if _, candidate := p.AutoDJConfiguration(); candidate != "" {
		t.Fatalf("candidate = %q, want consumed", candidate)
	}
}
