package ai

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fakeCommenter is a network-free Commenter for the worker tests.
type fakeCommenter struct {
	narratives  []Narrative
	comments    []Comment
	nErr, cErr  error
	calls       int
	lastCfg     CommentConfig
	digest      string
	digestErr   error
	digestCalls int
	lastDigest  ResultsDigestData

	compact      string
	compactErr   error
	compactCalls int
	lastCompact  []string

	rivalries    []Rivalry
	rivalriesErr error
	rivalryCalls int
	lastRivExist []Rivalry
}

func (f *fakeCommenter) DetectNarratives(ctx context.Context, h []RoundStanding) ([]Narrative, error) {
	return f.narratives, f.nErr
}

func (f *fakeCommenter) WriteComments(ctx context.Context, h []RoundStanding, n []Narrative, cfg CommentConfig) ([]Comment, error) {
	f.calls++
	f.lastCfg = cfg
	return f.comments, f.cErr
}

func (f *fakeCommenter) DigestResults(ctx context.Context, data ResultsDigestData, cfg CommentConfig) (string, error) {
	f.digestCalls++
	f.lastDigest = data
	return f.digest, f.digestErr
}

func (f *fakeCommenter) CompactNotes(ctx context.Context, notes []string, cfg CommentConfig) (string, error) {
	f.compactCalls++
	f.lastCompact = notes
	return f.compact, f.compactErr
}

func (f *fakeCommenter) UpdateRivalries(ctx context.Context, h []RoundStanding, dn string, existing []Rivalry, cfg CommentConfig) ([]Rivalry, error) {
	f.rivalryCalls++
	f.lastRivExist = existing
	return f.rivalries, f.rivalriesErr
}

func oneRound() []RoundStanding {
	return []RoundStanding{{
		Label: "2026-06-11",
		Ranks: []PlayerStanding{{UserID: 1, Name: "Joao", Position: 1, Total: 3}},
	}}
}

func TestCommentWorkerPassCachesSanitized(t *testing.T) {
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	fc := &fakeCommenter{
		comments: []Comment{{UserID: 1, Player: "Joao", Text: "you \x1b[31mfell\x1b[0m hard"}},
	}
	cache := NewCommentCache()
	mon := NewCommentMonitor("test", time.Hour)
	w := NewCommentWorker(CommentDeps{
		History: func() ([]RoundStanding, error) { return oneRound(), nil },
		Config:  func() CommentConfig { return CommentConfig{DefaultTone: "savage"} },
		Now:     func() time.Time { return now },
	}, fc, cache, mon, "", "")

	w.pass(context.Background())

	got := cache.All(now)
	c, ok := got[1]
	if !ok {
		t.Fatal("expected a comment cached for user 1")
	}
	if c.Text != "you fell hard" {
		t.Fatalf("expected ANSI stripped, got %q", c.Text)
	}
	// The director owns the cadence now: per-player comments never expire on a clock
	// (they persist until the next pass replaces them), so ExpiresAt is the zero time.
	if !c.ExpiresAt.IsZero() {
		t.Fatalf("ExpiresAt = %v, want zero (never expires)", c.ExpiresAt)
	}
	if w := mon.Status().Written; w != 1 {
		t.Fatalf("Written = %d, want 1", w)
	}
}

func TestCommentWorkerPassFeedsBackPriorComments(t *testing.T) {
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	fc := &fakeCommenter{comments: []Comment{{UserID: 1, Player: "Joao", Text: "you're cruising at the top"}}}
	cache := NewCommentCache()
	w := NewCommentWorker(CommentDeps{
		History: func() ([]RoundStanding, error) { return oneRound(), nil },
		Config:  func() CommentConfig { return CommentConfig{DefaultTone: "playful"} },
		Now:     func() time.Time { return now },
	}, fc, cache, NewCommentMonitor("t", time.Hour), "", "")

	// First pass has nothing cached yet — no prior-lines context.
	w.pass(context.Background())
	if len(fc.lastCfg.PriorComments) != 0 {
		t.Fatalf("first pass should carry no prior comments, got %+v", fc.lastCfg.PriorComments)
	}

	// Second pass must feed the first pass's line back so the model varies.
	w.pass(context.Background())
	if got := fc.lastCfg.PriorComments["Joao"]; got != "you're cruising at the top" {
		t.Fatalf("second pass PriorComments[Joao] = %q, want the first pass's line", got)
	}
}

func TestCommentWorkerRefreshesAutoRivalries(t *testing.T) {
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	fc := &fakeCommenter{
		comments:  []Comment{{UserID: 1, Player: "Joao", Text: "hi"}},
		rivalries: []Rivalry{{A: "Joao", B: "Ana", Note: "tied at the \x1b[31mtop\x1b[0m"}},
	}
	var saved []Rivalry
	w := NewCommentWorker(CommentDeps{
		History:       func() ([]RoundStanding, error) { return oneRound(), nil },
		Config:        func() CommentConfig { return CommentConfig{DefaultTone: "playful"} },
		Now:           func() time.Time { return now },
		AutoRivalries: func() []Rivalry { return []Rivalry{{A: "Joao", B: "Ana", Note: "old"}} },
		SetAutoRivalries: func(r []Rivalry) error {
			saved = r
			return nil
		},
	}, fc, NewCommentCache(), NewCommentMonitor("t", time.Hour), "", "")

	w.pass(context.Background())

	if fc.rivalryCalls != 1 {
		t.Fatalf("UpdateRivalries calls = %d, want 1", fc.rivalryCalls)
	}
	if len(fc.lastRivExist) != 1 || fc.lastRivExist[0].Note != "old" {
		t.Fatalf("existing rivalries not passed through: %+v", fc.lastRivExist)
	}
	if len(saved) != 1 {
		t.Fatalf("saved %d rivalries, want 1", len(saved))
	}
	if saved[0].Note != "tied at the top" {
		t.Fatalf("note not sanitized: %q", saved[0].Note)
	}
}

func TestCommentWorkerPassPersistsComments(t *testing.T) {
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	fc := &fakeCommenter{comments: []Comment{{UserID: 1, Player: "Joao", Text: "you \x1b[31mfell\x1b[0m"}}}
	var saved []Comment
	w := NewCommentWorker(CommentDeps{
		History:      func() ([]RoundStanding, error) { return oneRound(), nil },
		Config:       func() CommentConfig { return CommentConfig{DefaultTone: "playful"} },
		Now:          func() time.Time { return now },
		SaveComments: func(cs []Comment) error { saved = cs; return nil },
	}, fc, NewCommentCache(), NewCommentMonitor("t", time.Hour), "", "")

	w.pass(context.Background())

	if len(saved) != 1 || saved[0].Text != "you fell" {
		t.Fatalf("SaveComments should get the sanitized set, got %+v", saved)
	}
}

func TestCommentWorkerRunSkipsBootPassWhenCacheFilled(t *testing.T) {
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	fc := &fakeCommenter{comments: []Comment{{UserID: 1, Player: "Joao", Text: "regenerated"}}}
	cache := NewCommentCache()
	// Simulate persisted comments restored before Run.
	cache.Replace([]Comment{{UserID: 1, Player: "Joao", Text: "restored"}})
	w := NewCommentWorker(CommentDeps{
		History: func() ([]RoundStanding, error) { return oneRound(), nil },
		Config:  func() CommentConfig { return CommentConfig{DefaultTone: "playful"} },
		Now:     func() time.Time { return now },
	}, fc, cache, NewCommentMonitor("t", time.Hour), "", "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Run returns after the (skipped) boot pass without entering the loop
	w.Run(ctx)

	if fc.calls != 0 {
		t.Fatalf("boot pass must be skipped when cache is filled (WriteComments calls=%d)", fc.calls)
	}
	if got := cache.All(now)[1].Text; got != "restored" {
		t.Fatalf("restored comment should be untouched, got %q", got)
	}
}

func TestCommentWorkerSkipsEmptyHistory(t *testing.T) {
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	fc := &fakeCommenter{}
	w := NewCommentWorker(CommentDeps{
		History: func() ([]RoundStanding, error) { return nil, nil },
		Config:  func() CommentConfig { return CommentConfig{DefaultTone: "playful"} },
		Now:     func() time.Time { return now },
	}, fc, NewCommentCache(), NewCommentMonitor("t", time.Hour), "", "")

	w.pass(context.Background())

	if fc.calls != 0 {
		t.Fatalf("WriteComments should not be called with no history (calls=%d)", fc.calls)
	}
}

func TestCommentCacheTTLExpiry(t *testing.T) {
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	cache := NewCommentCache()
	cache.Replace([]Comment{{UserID: 1, Player: "Joao", Text: "hi", At: now, ExpiresAt: now.Add(time.Hour)}})

	if n := len(cache.All(now)); n != 1 {
		t.Fatalf("present before expiry: got %d", n)
	}
	if n := len(cache.All(now.Add(90 * time.Minute))); n != 1 {
		t.Fatalf("present within grace: got %d", n) // expiry+1h grace = now+2h
	}
	if n := len(cache.All(now.Add(3 * time.Hour))); n != 0 {
		t.Fatalf("dropped past grace: got %d", n)
	}
}

func TestCommentConfigToneFor(t *testing.T) {
	cfg := CommentConfig{DefaultTone: "playful", ToneByName: map[string]string{
		"Joao": "savage", "Maria": "mute",
	}}
	if got := cfg.toneFor("Joao"); got != "savage" {
		t.Errorf("Joao override: got %q", got)
	}
	if got := cfg.toneFor("Maria"); got != "mute" {
		t.Errorf("Maria mute: got %q", got)
	}
	if got := cfg.toneFor("Pedro"); got != "playful" {
		t.Errorf("Pedro default: got %q", got)
	}
}

func TestCommentWorkerDropsMuted(t *testing.T) {
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	fc := &fakeCommenter{comments: []Comment{
		{UserID: 1, Player: "Joao", Text: "you slipped"},
		{UserID: 2, Player: "Maria", Text: "should be dropped"},
	}}
	cache := NewCommentCache()
	w := NewCommentWorker(CommentDeps{
		History: func() ([]RoundStanding, error) { return oneRound(), nil },
		Config: func() CommentConfig {
			return CommentConfig{DefaultTone: "playful", ToneByName: map[string]string{"Maria": "mute"}}
		},
		Now: func() time.Time { return now },
	}, fc, cache, NewCommentMonitor("t", time.Hour), "", "")

	w.pass(context.Background())

	got := cache.All(now)
	if _, ok := got[1]; !ok {
		t.Error("Joao's comment should be cached")
	}
	if _, ok := got[2]; ok {
		t.Error("Maria is muted — her comment must be dropped")
	}
}

func TestCommentWorkerTriggerCoalesces(t *testing.T) {
	w := NewCommentWorker(CommentDeps{}, &fakeCommenter{}, NewCommentCache(),
		NewCommentMonitor("t", time.Hour), "", "")
	if !w.Trigger() {
		t.Fatal("first Trigger should succeed")
	}
	if w.Trigger() {
		t.Fatal("second Trigger should coalesce to false")
	}
}

func TestCommentPromptOverride(t *testing.T) {
	history := oneRound()
	narratives := []Narrative{{Type: "leader"}}

	// Default: built-in persona, plus the always-appended trailer + standings.
	def := commentPrompt(history, narratives, CommentConfig{DefaultTone: "playful"})
	if !strings.Contains(def, "You are BETanIA") {
		t.Error("default prompt should use the built-in persona body")
	}
	if !strings.Contains(def, "submit_comments") {
		t.Error("default prompt missing the submit_comments trailer")
	}
	if !strings.Contains(def, "STANDINGS + HISTORY (JSON)") {
		t.Error("default prompt missing the standings JSON block")
	}

	// Override: custom body replaces the persona, trailer + standings still appended.
	const body = "ACT AS A PIRATE. Roast every player in pirate-speak."
	ov := commentPrompt(history, narratives, CommentConfig{DefaultTone: "playful", PromptOverride: body})
	if !strings.Contains(ov, body) {
		t.Error("override prompt should contain the custom instruction body")
	}
	if strings.Contains(ov, "You are BETanIA") {
		t.Error("override prompt must NOT contain the built-in persona body")
	}
	if !strings.Contains(ov, "submit_comments") {
		t.Error("override prompt missing the submit_comments trailer")
	}
	if !strings.Contains(ov, "STANDINGS + HISTORY (JSON)") {
		t.Error("override prompt missing the standings JSON block")
	}
}

func TestRegenerateOneUpsertsJustThatPlayer(t *testing.T) {
	now := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	fc := &fakeCommenter{comments: []Comment{
		{UserID: 1, Player: "Joao", Text: "fresh joao line"},
		{UserID: 2, Player: "Ana", Text: "fresh ana line"},
	}}
	cache := NewCommentCache()
	cache.Replace([]Comment{
		{UserID: 1, Player: "Joao", Text: "OLD joao", ExpiresAt: now.Add(time.Hour)},
		{UserID: 2, Player: "Ana", Text: "OLD ana", ExpiresAt: now.Add(time.Hour)},
	})
	w := NewCommentWorker(CommentDeps{
		History: func() ([]RoundStanding, error) {
			return []RoundStanding{{Label: "d", Ranks: []PlayerStanding{{UserID: 1, Name: "Joao"}, {UserID: 2, Name: "Ana"}}}}, nil
		},
		Config: func() CommentConfig { return CommentConfig{DefaultTone: "playful"} },
		Now:    func() time.Time { return now },
	}, fc, cache, NewCommentMonitor("t", time.Hour), "", "")

	c, err := w.RegenerateOne(context.Background(), 1, "")
	if err != nil {
		t.Fatalf("RegenerateOne: %v", err)
	}
	if c.Text != "fresh joao line" {
		t.Errorf("returned text = %q", c.Text)
	}
	got := cache.All(now)
	if got[1].Text != "fresh joao line" {
		t.Errorf("player 1 not upserted: %q", got[1].Text)
	}
	if got[2].Text != "OLD ana" {
		t.Errorf("player 2 should be untouched, got %q", got[2].Text)
	}

	// Unknown player -> error, cache unchanged.
	if _, err := w.RegenerateOne(context.Background(), 99, ""); err == nil {
		t.Error("expected error for a player with no comment")
	}
}
