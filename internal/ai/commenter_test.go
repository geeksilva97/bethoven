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

	houseCompact      string
	houseCompactErr   error
	houseCompactCalls int
	lastHouseCompact  []string

	rivalries    []Rivalry
	rivalriesErr error
	rivalryCalls int
	lastRivExist []Rivalry

	card      string
	cardErr   error
	cardCalls int
	lastCard  CardDigestData
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

func (f *fakeCommenter) CompactHouseNotes(ctx context.Context, notes []string, cfg CommentConfig) (string, error) {
	f.houseCompactCalls++
	f.lastHouseCompact = notes
	return f.houseCompact, f.houseCompactErr
}

func (f *fakeCommenter) UpdateRivalries(ctx context.Context, h []RoundStanding, dn string, existing []Rivalry, cfg CommentConfig) ([]Rivalry, error) {
	f.rivalryCalls++
	f.lastRivExist = existing
	return f.rivalries, f.rivalriesErr
}

func (f *fakeCommenter) GeneratePlayerCard(ctx context.Context, data CardDigestData, cfg CommentConfig) (string, error) {
	f.cardCalls++
	f.lastCard = data
	return f.card, f.cardErr
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

// TestRivalryPromptCarriesHouseNotes verifies the admin's per-player and pool house
// notes reach the auto-rivalry detection prompt, so BETanIA can seed a rivalry from a
// real-world feud the admin flagged even before the standings reflect it.
func TestRivalryPromptCarriesHouseNotes(t *testing.T) {
	cfg := CommentConfig{
		DefaultTone: "playful",
		PlayerNotes: []PlayerNote{{Player: "Joao", Text: "office nemesis of Ana since the Copa office pool"}},
		Notes:       []string{"the pool splits into Rio vs Sao Paulo camps"},
	}
	p := rivalryPrompt(oneRound(), "", nil, cfg)
	if !strings.Contains(p, "ADMIN HOUSE NOTES") {
		t.Error("rivalry prompt must include the admin house-notes block")
	}
	if !strings.Contains(p, "About Joao: office nemesis of Ana") {
		t.Error("rivalry prompt must carry per-player notes with attribution")
	}
	if !strings.Contains(p, "Rio vs Sao Paulo") {
		t.Error("rivalry prompt must carry pool-wide general notes")
	}
	// With no notes set, the block is omitted entirely (no empty header).
	if bare := rivalryPrompt(oneRound(), "", nil, CommentConfig{DefaultTone: "playful"}); strings.Contains(bare, "ADMIN HOUSE NOTES") {
		t.Error("rivalry prompt must omit the house-notes block when there are none")
	}
}

// TestRivalryPromptCarriesAdminRivalries verifies the auto-rivalry DETECTION prompt
// loads the admin's curated rivalries as locked context (so BETanIA complements rather
// than re-proposes them), while NOT double-listing her own auto pairs.
func TestRivalryPromptCarriesAdminRivalries(t *testing.T) {
	existing := []Rivalry{{A: "Joao", B: "Ana", Note: "her own auto feud"}} // BETanIA's auto tier
	cfg := CommentConfig{
		// The merged set CommentConfig produces: an admin pair + the auto pair.
		Rivalries: []Rivalry{
			{A: "Bruno", B: "Carla", Note: "admin-declared office rivalry"},
			{A: "Joao", B: "Ana", Note: "her own auto feud"},
		},
	}
	// "locked feuds the admin tracks" is unique to the rendered block (the phrase
	// "ADMIN-CURATED RIVALRIES" also appears in rule 6, so it's not a reliable marker).
	const blockMarker = "locked feuds the admin tracks"
	p := rivalryPrompt(oneRound(), "", existing, cfg)
	if !strings.Contains(p, blockMarker) || !strings.Contains(p, "Bruno vs Carla: admin-declared office rivalry") {
		t.Error("detection prompt must load admin-curated rivalries as locked context")
	}
	if !strings.Contains(p, "do NOT re-propose these pairs") {
		t.Error("detection prompt must instruct BETanIA not to duplicate admin pairs")
	}
	// The auto pair (Joao/Ana) must appear under YOUR CURRENT RIVALRIES, not be
	// re-listed as an admin one.
	if strings.Contains(p, "Joao vs Ana: her own auto feud") {
		t.Error("BETanIA's own auto pair must not be double-listed as an admin rivalry")
	}
	// No admin-only rivalries ⇒ no locked block (rule 6's mention doesn't count).
	autoOnly := CommentConfig{Rivalries: existing}
	if bare := rivalryPrompt(oneRound(), "", existing, autoOnly); strings.Contains(bare, blockMarker) {
		t.Error("detection prompt must omit the admin-rivalry block when there are none")
	}
}

// TestSavageStyleIsGervais verifies the SAVAGE tone injects the genuinely-cutting
// Ricky Gervais roast style — whenever savage is the pool default OR any single player
// is overridden to savage — and stays absent for a purely playful pool.
func TestSavageStyleIsGervais(t *testing.T) {
	history := oneRound()

	// Pool default savage ⇒ style present.
	if p := commentPrompt(history, nil, CommentConfig{DefaultTone: "savage"}); !strings.Contains(p, "SAVAGE STYLE") || !strings.Contains(p, "Ricky Gervais") {
		t.Error("default-savage prompt must carry the Gervais SAVAGE STYLE block")
	}
	// A single per-player savage override on an otherwise playful pool ⇒ still present.
	perPlayer := CommentConfig{DefaultTone: "playful", ToneByName: map[string]string{"Joao": "savage"}}
	if p := commentPrompt(history, nil, perPlayer); !strings.Contains(p, "SAVAGE STYLE") {
		t.Error("a per-player savage override must trigger the SAVAGE STYLE block")
	}
	// Purely playful ⇒ no savage style at all.
	if p := commentPrompt(history, nil, CommentConfig{DefaultTone: "playful"}); strings.Contains(p, "SAVAGE STYLE") {
		t.Error("a fully-playful pool must not carry the savage style block")
	}
	// The roast must keep the hard target boundary (game behaviour, not the person).
	if p := commentPrompt(history, nil, CommentConfig{DefaultTone: "savage"}); !strings.Contains(p, "POOL LIFE ONLY") {
		t.Error("savage style must keep the game-behaviour-only target boundary")
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

// TestAdminContextAlwaysAppendedFresh guards the fix for the bug where house notes were
// baked into the stored prompt override (and so never updated): the admin context is a
// fresh, always-appended tier — present under an override, and NOT part of the persona
// body that seeds the override editor (DefaultCommentPrompt).
func TestAdminContextAlwaysAppendedFresh(t *testing.T) {
	history := oneRound()
	cfg := CommentConfig{
		DefaultTone: "playful",
		PlayerNotes: []PlayerNote{{Player: "Joao", Text: "always bets on Brazil no matter what"}},
		Notes:       []string{"the pool started as an office bet"},
		Rivalries:   []Rivalry{{A: "Joao", B: "Ana", Note: "trading the lead all tournament"}},
	}

	// Fresh in the built-in (no override) path.
	def := commentPrompt(history, nil, cfg)
	for _, want := range []string{"ADMIN-PROVIDED CONTEXT", "About Joao: always bets on Brazil", "office bet", "Rivalry between Joao and Ana"} {
		if !strings.Contains(def, want) {
			t.Errorf("default prompt missing admin context %q", want)
		}
	}

	// Still present under a prompt override — loaded at generation time, not frozen.
	ovCfg := cfg
	ovCfg.PromptOverride = "Talk like a pirate."
	ov := commentPrompt(history, nil, ovCfg)
	for _, want := range []string{"About Joao: always bets on Brazil", "Rivalry between Joao and Ana"} {
		if !strings.Contains(ov, want) {
			t.Errorf("override prompt must still carry fresh admin context %q", want)
		}
	}

	// The override-editor seed must NOT bake the notes in — else editing the persona
	// freezes a stale snapshot of them into the stored prompt (the reported bug).
	seed := DefaultCommentPrompt(cfg)
	for _, notWant := range []string{"About Joao", "office bet", "Rivalry between Joao and Ana", "ADMIN-PROVIDED CONTEXT"} {
		if strings.Contains(seed, notWant) {
			t.Errorf("DefaultCommentPrompt must not bake in admin context, but contained %q", notWant)
		}
	}
}

// The per-player comment prompt must surface no-pick/tenure grounding when any player
// has a caveat, telling BETanIA a blank isn't a wrong pick — and stay silent (no
// block) for a fully-participating pool. Always appended, even under an override.
func TestCommentPromptParticipation(t *testing.T) {
	history := oneRound()
	cfg := CommentConfig{
		DefaultTone: "savage",
		Participation: []PlayerParticipation{
			{Name: "Zoe", MatchesAvailable: 8, MatchesBet: 2, MatchesSkipped: 6, JoinedLate: true, RegisteredAt: "Jun 18", MatchesBeforeJoining: 4, RecentSkips: 5},
		},
	}
	p := commentPrompt(history, nil, cfg)
	if !strings.Contains(p, "NO-PICK IS NOT A WRONG PICK") {
		t.Error("comment prompt must instruct that a skipped game is not a wrong pick")
	}
	for _, want := range []string{`"matches_skipped":6`, `"joined_late":true`, `"registered_at":"Jun 18"`, `"recent_skips":5`} {
		if !strings.Contains(p, want) {
			t.Errorf("participation JSON missing %s", want)
		}
	}
	// Survives an admin override (it's a correctness guard, not a style choice).
	if !strings.Contains(commentPrompt(history, nil, CommentConfig{PromptOverride: "Talk like a pirate.", Participation: cfg.Participation}), "NO-PICK IS NOT A WRONG PICK") {
		t.Error("participation grounding must persist under a prompt override")
	}
	// No caveats ⇒ no participation block at all.
	if strings.Contains(commentPrompt(history, nil, CommentConfig{DefaultTone: "playful"}), "PARTICIPATION & TENURE") {
		t.Error("a fully-participating pool must produce no participation block")
	}
}

// The live director gets the same no-pick/tenure grounding, so it never roasts a
// bottom-of-table or zero-points player as a bad predictor when they're sitting out.
func TestLiveCommentPromptParticipation(t *testing.T) {
	sit := LiveSituation{Matches: []LiveMatchInfo{{TeamA: "A", TeamB: "B", ScoreA: 1, ScoreB: 0, Clock: "30'"}}}
	cfg := CommentConfig{
		DefaultTone:   "playful",
		Participation: []PlayerParticipation{{Name: "Ghost", MatchesAvailable: 10, MatchesBet: 0, MatchesSkipped: 10, NeverPicked: true}},
	}
	p := liveCommentPrompt(sit, nil, cfg)
	if !strings.Contains(p, "NO-PICK IS NOT A WRONG PICK") || !strings.Contains(p, `"never_picked":true`) {
		t.Error("live prompt must carry the no-pick/tenure grounding")
	}
	if strings.Contains(liveCommentPrompt(sit, nil, CommentConfig{DefaultTone: "playful"}), "PARTICIPATION & TENURE") {
		t.Error("no participation ⇒ no block in the live prompt")
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
