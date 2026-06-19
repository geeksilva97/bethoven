package ai

import (
	"context"
	"testing"
	"time"
)

// fakeCommenter is a network-free Commenter for the worker tests.
type fakeCommenter struct {
	narratives []Narrative
	comments   []Comment
	nErr, cErr error
	calls      int
}

func (f *fakeCommenter) DetectNarratives(ctx context.Context, h []RoundStanding) ([]Narrative, error) {
	return f.narratives, f.nErr
}

func (f *fakeCommenter) WriteComments(ctx context.Context, h []RoundStanding, n []Narrative, tone, self string) ([]Comment, error) {
	f.calls++
	return f.comments, f.cErr
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
		Tone:    func() string { return "savage" },
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
		Tone:    func() string { return "playful" },
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
