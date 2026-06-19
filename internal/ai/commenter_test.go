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
	}, fc, cache, mon, "", time.Hour, "")

	w.pass(context.Background())

	got := cache.All(now)
	c, ok := got[1]
	if !ok {
		t.Fatal("expected a comment cached for user 1")
	}
	if c.Text != "you fell hard" {
		t.Fatalf("expected ANSI stripped, got %q", c.Text)
	}
	if !c.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("ExpiresAt = %v, want %v", c.ExpiresAt, now.Add(time.Hour))
	}
	if w := mon.Status().Written; w != 1 {
		t.Fatalf("Written = %d, want 1", w)
	}
}

func TestCommentWorkerSkipsEmptyHistory(t *testing.T) {
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	fc := &fakeCommenter{}
	w := NewCommentWorker(CommentDeps{
		History: func() ([]RoundStanding, error) { return nil, nil },
		Config:  func() CommentConfig { return CommentConfig{DefaultTone: "playful"} },
		Now:     func() time.Time { return now },
	}, fc, NewCommentCache(), NewCommentMonitor("t", time.Hour), "", time.Hour, "")

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
	}, fc, cache, NewCommentMonitor("t", time.Hour), "", time.Hour, "")

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
		NewCommentMonitor("t", time.Hour), "", time.Hour, "")
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
