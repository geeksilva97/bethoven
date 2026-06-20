package ai

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeLiveCommenter records its calls and returns a distinct line each time, so the
// worker test can assert when (and with what recent history) it was invoked.
type fakeLiveCommenter struct {
	calls      int
	lastRecent []string
}

func (f *fakeLiveCommenter) WriteLiveComment(ctx context.Context, sit LiveSituation, recent []string, cfg CommentConfig) (LiveOutput, error) {
	f.calls++
	f.lastRecent = append([]string(nil), recent...)
	return LiveOutput{Comment: fmt.Sprintf("line %d", f.calls)}, nil
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
	w := NewLiveCommentWorker(deps, fake, cache, "", 5*time.Minute, "")
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

// scriptedDirector returns a fixed LiveOutput, for asserting the worker's handling
// of mood, the stay-silent (empty comment) path, and per-player regen requests.
type scriptedDirector struct{ out LiveOutput }

func (d *scriptedDirector) WriteLiveComment(ctx context.Context, sit LiveSituation, recent []string, cfg CommentConfig) (LiveOutput, error) {
	return d.out, nil
}

// TestDirectorMoodSilenceAndRegen drives one director pass over a just-finished
// match where she stays silent: the line is suppressed but the signature is still
// recorded, the mood is applied, and the named roasts are queued for regeneration.
func TestDirectorMoodSilenceAndRegen(t *testing.T) {
	now := time.Date(2026, 6, 20, 18, 0, 0, 0, time.UTC)
	sit := LiveSituation{Settled: []LiveSettled{{TeamA: "A", TeamB: "B", Score: "1-0"}}}

	var moodSet string
	regened := make(chan string, 8)
	deps := LiveCommentDeps{
		Situation:    func() (LiveSituation, bool, error) { return sit, true, nil },
		Config:       func() CommentConfig { return CommentConfig{} },
		Now:          func() time.Time { return now },
		SetMood:      func(m string) error { moodSet = m; return nil },
		RegenComment: func(ctx context.Context, name string) error { regened <- name; return nil },
	}
	dir := &scriptedDirector{out: LiveOutput{Comment: "", Mood: "cocky", Regen: []string{"Edy", "Miguel"}}}
	cache := NewLiveCommentCache()
	w := NewLiveCommentWorker(deps, dir, cache, "BETanIA 🤖", 5*time.Minute, "")
	w.pass(context.Background())

	if moodSet != "cocky" {
		t.Fatalf("mood = %q, want cocky", moodSet)
	}
	if got := cache.Current(now); got != "" {
		t.Fatalf("stay-silent pass should show no line, got %q", got)
	}
	if sig, _ := cache.snapshot(); sig == "" {
		t.Fatal("stay-silent pass should still record the signature (so it isn't re-asked every tick)")
	}
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case n := <-regened:
			seen[n] = true
		case <-time.After(2 * time.Second):
			t.Fatal("expected roast regeneration was not fired")
		}
	}
	if !seen["Edy"] || !seen["Miguel"] {
		t.Fatalf("regenerated = %v, want Edy + Miguel", seen)
	}
}

// TestDirectorRegenCapAndFloor checks the rate limits on her regen power: at most
// maxRegenPerPass per pass, and the same player not re-fired within commentRegenFloor.
func TestDirectorRegenCapAndFloor(t *testing.T) {
	now := time.Date(2026, 6, 20, 18, 0, 0, 0, time.UTC)
	sit := LiveSituation{Settled: []LiveSettled{{TeamA: "A", TeamB: "B", Score: "1-0"}}}
	regened := make(chan string, 16)
	names := []string{"p1", "p2", "p3", "p4", "p5"}
	deps := LiveCommentDeps{
		Situation:    func() (LiveSituation, bool, error) { return sit, true, nil },
		Config:       func() CommentConfig { return CommentConfig{} },
		Now:          func() time.Time { return now },
		RegenComment: func(ctx context.Context, name string) error { regened <- name; return nil },
	}
	dir := &scriptedDirector{out: LiveOutput{Comment: "x", Regen: names}}
	w := NewLiveCommentWorker(deps, dir, NewLiveCommentCache(), "BETanIA", 5*time.Minute, "")

	w.pass(context.Background())
	drain := func() map[string]bool {
		out := map[string]bool{}
		for {
			select {
			case n := <-regened:
				out[n] = true
			case <-time.After(300 * time.Millisecond):
				return out
			}
		}
	}
	first := drain()
	if len(first) != maxRegenPerPass {
		t.Fatalf("first pass fired %d regens, want cap %d", len(first), maxRegenPerPass)
	}

	// A later pass within the floor must NOT re-fire a player regenerated last pass.
	// Force a fresh signature so the pass runs, advance time past the floor so the
	// 3 already-regenerated players are eligible again — only the 2 leftovers + those
	// 3 are candidates, still capped at maxRegenPerPass and never the same one twice.
	sit.Settled[0].Score = "2-0"
	now = now.Add(commentRegenFloor / 2) // still within the per-player floor
	w.pass(context.Background())
	second := drain()
	for n := range second {
		if first[n] {
			t.Fatalf("player %q regenerated again within the floor window", n)
		}
	}
}

func TestLiveCacheSnapshotRoundTrip(t *testing.T) {
	now := time.Date(2026, 6, 19, 23, 0, 0, 0, time.UTC)
	c := NewLiveCommentCache()
	c.set("first line", "sig-1", now.Add(10*time.Minute))
	c.set("second line", "sig-2", now.Add(10*time.Minute))

	snap := c.SnapshotJSON()
	if snap == "" {
		t.Fatal("snapshot should not be empty")
	}

	// A fresh cache restores the current line + signature + history.
	c2 := NewLiveCommentCache()
	c2.LoadJSON(snap)
	if got := c2.Current(now); got != "second line" {
		t.Errorf("restored current = %q, want %q", got, "second line")
	}
	sig, hist := c2.snapshot()
	if sig != "sig-2" {
		t.Errorf("restored sig = %q, want sig-2", sig)
	}
	if len(hist) != 2 || hist[0] != "first line" {
		t.Errorf("restored history = %v", hist)
	}

	// Empty / malformed input is a no-op (no panic, stays empty).
	c3 := NewLiveCommentCache()
	c3.LoadJSON("")
	c3.LoadJSON("{not json")
	if c3.Current(now) != "" {
		t.Error("bad input should leave cache empty")
	}
	if NewLiveCommentCache().SnapshotJSON() != "" {
		t.Error("empty cache should snapshot to empty string")
	}
}

func TestLiveCommentPromptVarietyAndRivalries(t *testing.T) {
	cfg := CommentConfig{
		DefaultTone: "savage",
		Rivalries:   []Rivalry{{A: "miguel", B: "Edy", Note: "Caldense derby"}},
	}
	sit := LiveSituation{
		Matches:   []LiveMatchInfo{{TeamA: "Scotland", TeamB: "Morocco", ScoreB: 1, Picks: []LivePickInfo{{Player: "Marcello", Pred: "0-1", LivePoints: 7}}}},
		Standings: []LiveStanding{{Player: "miguel", Position: 1, Total: 66}, {Player: "Edy", Position: 2, Total: 64}},
	}
	p := liveCommentPrompt(sit, []string{"Marcello nailed it again"}, cfg)

	if !strings.Contains(p, "RIVALRIES") || !strings.Contains(p, "Caldense derby") {
		t.Error("prompt should carry the admin rivalries")
	}
	if !strings.Contains(p, "TITLE RACE") {
		t.Error("prompt should offer the title-race angle")
	}
	if !strings.Contains(p, "SPREAD THE LOVE") {
		t.Error("prompt should push anti-fixation / variety")
	}
	if !strings.Contains(p, "Marcello nailed it again") {
		t.Error("recent lines should be fed back for anti-repeat")
	}
}

func TestHalftimeFocus(t *testing.T) {
	none := LiveSituation{}
	if halftimeFocus(none) {
		t.Error("empty situation should not be halftime focus")
	}
	ht := LiveSituation{Matches: []LiveMatchInfo{{TeamA: "A", TeamB: "B", Phase: "halftime"}}}
	if !halftimeFocus(ht) {
		t.Error("single halftime match should be halftime focus")
	}
	mixed := LiveSituation{Matches: []LiveMatchInfo{{Phase: "halftime"}, {Phase: ""}}}
	if halftimeFocus(mixed) {
		t.Error("a match in open play means not halftime focus")
	}
}

func TestLiveCommentPromptSwitchesAtHalftime(t *testing.T) {
	cfg := CommentConfig{DefaultTone: "savage"}

	open := LiveSituation{Matches: []LiveMatchInfo{{TeamA: "Scotland", TeamB: "Morocco", ScoreA: 0, ScoreB: 1}}}
	p := liveCommentPrompt(open, nil, cfg)
	if !strings.Contains(p, "RIGHT NOW") {
		t.Error("open-play prompt should be about the match right now")
	}
	if strings.Contains(p, "LEADERBOARD DYNAMICS") {
		t.Error("open-play prompt should NOT pivot to leaderboard dynamics")
	}

	half := LiveSituation{
		Matches:   []LiveMatchInfo{{TeamA: "Scotland", TeamB: "Morocco", ScoreA: 0, ScoreB: 1, Phase: "halftime"}},
		Standings: []LiveStanding{{Player: "miguel", Position: 1, Total: 66}, {Player: "Edy", Position: 2, Total: 64}},
	}
	ph := liveCommentPrompt(half, nil, cfg)
	if !strings.Contains(ph, "HALFTIME") || !strings.Contains(ph, "LEADERBOARD DYNAMICS") {
		t.Error("halftime prompt should pivot to leaderboard dynamics")
	}
	if strings.Contains(ph, "play-by-play") {
		t.Error("halftime prompt should NOT ask for play-by-play")
	}
	// The standings snapshot must reach the model.
	if !strings.Contains(ph, "miguel") {
		t.Error("halftime prompt should carry the standings snapshot")
	}

	// Admin override still pivots at halftime.
	po := liveCommentPrompt(half, nil, CommentConfig{PromptOverride: "Talk like a pirate."})
	if !strings.Contains(po, "pirate") || !strings.Contains(po, "LEADERBOARD DYNAMICS") {
		t.Errorf("override+halftime should keep voice and pivot; got:\n%s", po)
	}
}
