package ai

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDigestSignatureChangesWithResults(t *testing.T) {
	a := ResultsDigestData{Matches: []FinishedMatchDigest{{TeamA: "X", TeamB: "Y", Score: "1-0", Picks: []DigestPick{{}}}}}
	b := ResultsDigestData{Matches: []FinishedMatchDigest{{TeamA: "X", TeamB: "Y", Score: "1-1", Picks: []DigestPick{{}}}}}
	c := ResultsDigestData{Matches: []FinishedMatchDigest{{TeamA: "X", TeamB: "Y", Score: "1-0", Picks: []DigestPick{{}, {}}}}}
	if digestSignature(a) == digestSignature(b) {
		t.Error("score change should change the signature")
	}
	if digestSignature(a) == digestSignature(c) {
		t.Error("a new pick should change the signature")
	}
	if digestSignature(a) != digestSignature(a) {
		t.Error("signature must be stable")
	}
}

// memDigestStore is a tiny in-memory stand-in for the persisted derived-notes
// tier, so the worker's append/feed/sig logic can be tested without the service.
type memDigestStore struct {
	notes []string
	sig   string
}

func (d *memDigestStore) load() (string, string) { return joinNotes(d.notes), d.sig }
func (d *memDigestStore) add(text, sig string) error {
	d.notes = append(d.notes, text)
	d.sig = sig
	return nil
}

func joinNotes(ns []string) string {
	out := ""
	for i, n := range ns {
		if i > 0 {
			out += "\n"
		}
		out += n
	}
	return out
}

func TestCommentWorkerDerivesNotesAndFeedsThem(t *testing.T) {
	now := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	store := &memDigestStore{}
	results := ResultsDigestData{Matches: []FinishedMatchDigest{{TeamA: "Brazil", TeamB: "Spain", Score: "2-1"}}}

	fc := &fakeCommenter{
		comments: []Comment{{UserID: 1, Player: "Joao", Text: "nice call"}},
		digest:   "Brazil edged Spain 2-1; the story of the night.",
	}
	w := NewCommentWorker(CommentDeps{
		History:        func() ([]RoundStanding, error) { return oneRound(), nil },
		Config:         func() CommentConfig { return CommentConfig{DefaultTone: "playful"} },
		Now:            func() time.Time { return now },
		Results:        func() (ResultsDigestData, error) { return results, nil },
		DerivedNotes:   store.load,
		AddDerivedNote: store.add,
	}, fc, NewCommentCache(), NewCommentMonitor("t", time.Hour), "", time.Hour, "")

	// First pass: new results ⇒ one digest call, note appended + fed to the writer.
	w.pass(context.Background())
	if fc.digestCalls != 1 {
		t.Fatalf("digest calls = %d, want 1", fc.digestCalls)
	}
	if len(store.notes) != 1 || store.notes[0] != fc.digest {
		t.Fatalf("note not persisted: %+v", store.notes)
	}
	if fc.lastCfg.DerivedNotes != fc.digest {
		t.Fatalf("derived notes not fed to writer: %q", fc.lastCfg.DerivedNotes)
	}

	// Second pass with the SAME results ⇒ no new digest call (sig unchanged).
	w.pass(context.Background())
	if fc.digestCalls != 1 {
		t.Fatalf("digest re-called for unchanged results: %d", fc.digestCalls)
	}

	// New result ⇒ a fresh digest + a second note.
	results.Matches = append(results.Matches, FinishedMatchDigest{TeamA: "Argentina", TeamB: "France", Score: "0-0"})
	fc.digest = "Argentina and France played out a goalless draw."
	w.pass(context.Background())
	if fc.digestCalls != 2 {
		t.Fatalf("digest calls after new result = %d, want 2", fc.digestCalls)
	}
	if len(store.notes) != 2 {
		t.Fatalf("notes after new result = %d, want 2", len(store.notes))
	}
}

func TestRecentLiveComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ai_comments.log")
	lines := "" +
		`{"at":"2026-06-20T09:00:00Z","source":"live_comment","text":"kickoff energy"}` + "\n" +
		`{"at":"2026-06-20T09:30:00Z","source":"comment","player":"Joao","text":"per-player, ignore"}` + "\n" +
		`{"at":"2026-06-20T09:45:00Z","source":"live_comment","text":"Brazil strikes first"}` + "\n" +
		`{"at":"2026-06-20T08:00:00Z","source":"live_comment","text":"too old, before since"}` + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	since := time.Date(2026, 6, 20, 8, 30, 0, 0, time.UTC)
	got := RecentLiveComments(path, since, 10)
	want := []string{"kickoff energy", "Brazil strikes first"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}

	// Cap keeps only the most recent.
	if capped := RecentLiveComments(path, since, 1); len(capped) != 1 || capped[0] != "Brazil strikes first" {
		t.Errorf("cap=1 gave %v", capped)
	}

	// Missing file ⇒ nil, no panic.
	if RecentLiveComments(filepath.Join(t.TempDir(), "nope.log"), since, 10) != nil {
		t.Error("missing file should give nil")
	}
}
