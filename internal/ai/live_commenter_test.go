package ai

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// fakeLiveCommenter records its calls and returns a distinct line each time, so the
// worker test can assert when (and with what recent history) it was invoked.
type fakeLiveCommenter struct {
	calls      int
	lastRecent []string
}

func (f *fakeLiveCommenter) WriteLiveComment(ctx context.Context, sit LiveSituation, recent []string, cfg CommentConfig) (string, error) {
	f.calls++
	f.lastRecent = append([]string(nil), recent...)
	return fmt.Sprintf("line %d", f.calls), nil
}

// TestLiveCommentWorkerPass drives the worker through first-gen, an unchanged tick
// (no regen), a changed-after-floor tick (regen with recent history fed back), and
// a game-over tick (cache cleared).
func TestLiveCommentWorkerPass(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	sit := LiveSituation{Matches: []LiveMatchInfo{{TeamA: "A", TeamB: "B", ScoreA: 0, ScoreB: 0, Clock: "10'"}}}
	isLive := true

	deps := LiveCommentDeps{
		Situation: func() (LiveSituation, bool, error) { return sit, isLive, nil },
		Config:    func() CommentConfig { return CommentConfig{DefaultTone: "playful"} },
		Now:       func() time.Time { return now },
	}
	fake := &fakeLiveCommenter{}
	cache := NewLiveCommentCache()
	w := NewLiveCommentWorker(deps, fake, cache, 5*time.Minute, "")
	ctx := context.Background()

	// 1) First pass: generates the opening line, no recent history.
	w.pass(ctx)
	if fake.calls != 1 {
		t.Fatalf("first pass: calls = %d, want 1", fake.calls)
	}
	if got := cache.Current(now); got != "line 1" {
		t.Fatalf("first pass: current = %q, want %q", got, "line 1")
	}
	if len(fake.lastRecent) != 0 {
		t.Errorf("first pass: recent = %v, want empty", fake.lastRecent)
	}

	// 2) Same situation, same instant: nothing changed → no regeneration.
	w.pass(ctx)
	if fake.calls != 1 {
		t.Fatalf("unchanged pass: calls = %d, want still 1", fake.calls)
	}

	// 3) Score changes but we're still inside the floor window → suppressed (the
	//    "can a change get stuck?" case): no regeneration yet.
	sit.Matches[0].ScoreA = 1
	now = now.Add(liveFloor / 2)
	w.pass(ctx)
	if fake.calls != 1 {
		t.Fatalf("within-floor change: calls = %d, want still 1 (suppressed)", fake.calls)
	}

	// 4) Same change, now past the floor → it finally regenerates (the stale cached
	//    signature kept `changed` true), feeding the prior line back as history.
	now = now.Add(liveFloor)
	w.pass(ctx)
	if fake.calls != 2 {
		t.Fatalf("post-floor change: calls = %d, want 2", fake.calls)
	}
	if got := cache.Current(now); got != "line 2" {
		t.Errorf("post-floor change: current = %q, want %q", got, "line 2")
	}
	if len(fake.lastRecent) != 1 || fake.lastRecent[0] != "line 1" {
		t.Errorf("post-floor change: recent = %v, want [line 1]", fake.lastRecent)
	}

	// 5) Nothing changes, but the heartbeat elapses → regenerate anyway (so a quiet
	//    0-0 grind still gets a fresh take), with both prior lines as history.
	now = now.Add(w.heartbeat + time.Second)
	w.pass(ctx)
	if fake.calls != 3 {
		t.Fatalf("heartbeat pass: calls = %d, want 3", fake.calls)
	}
	if len(fake.lastRecent) != 2 || fake.lastRecent[1] != "line 2" {
		t.Errorf("heartbeat pass: recent = %v, want [line 1, line 2]", fake.lastRecent)
	}

	// 6) Game over: the worker clears the line and the rolling history.
	isLive = false
	now = now.Add(time.Minute)
	w.pass(ctx)
	if fake.calls != 3 {
		t.Errorf("game-over pass: calls = %d, want still 3 (no generation)", fake.calls)
	}
	if got := cache.Current(now); got != "" {
		t.Errorf("game-over pass: current = %q, want empty (cleared)", got)
	}
	if sig, hist := cache.snapshot(); sig != "" || len(hist) != 0 {
		t.Errorf("game-over pass: cache not cleared (sig=%q hist=%v)", sig, hist)
	}
}
