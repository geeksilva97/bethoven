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
	w := NewLiveCommentWorker(deps, fake, cache, "BETanIA", 5*time.Minute, "")
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

	// 3) Score changes and the floor has elapsed → regenerate, feeding the prior
	//    line back as recent history so the model won't repeat itself.
	sit.Matches[0].ScoreA = 1
	now = now.Add(liveFloor + time.Second)
	w.pass(ctx)
	if fake.calls != 2 {
		t.Fatalf("changed pass: calls = %d, want 2", fake.calls)
	}
	if got := cache.Current(now); got != "line 2" {
		t.Errorf("changed pass: current = %q, want %q", got, "line 2")
	}
	if len(fake.lastRecent) != 1 || fake.lastRecent[0] != "line 1" {
		t.Errorf("changed pass: recent = %v, want [line 1]", fake.lastRecent)
	}

	// 4) Game over: the worker clears the line and the rolling history.
	isLive = false
	now = now.Add(time.Minute)
	w.pass(ctx)
	if fake.calls != 2 {
		t.Errorf("game-over pass: calls = %d, want still 2 (no generation)", fake.calls)
	}
	if got := cache.Current(now); got != "" {
		t.Errorf("game-over pass: current = %q, want empty (cleared)", got)
	}
	if sig, hist := cache.snapshot(); sig != "" || len(hist) != 0 {
		t.Errorf("game-over pass: cache not cleared (sig=%q hist=%v)", sig, hist)
	}
}
