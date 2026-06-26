package ai

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// memDigestStore is a tiny in-memory stand-in for the per-game derived-notes tier,
// so the worker's one-note-per-game loop can be tested without the service. pending
// is the queue of games still missing a note; add() drains the matching entry.
type memDigestStore struct {
	notes   []string
	pending []ResultsDigestData
}

func (d *memDigestStore) load() string { return joinNotes(d.notes) }
func (d *memDigestStore) pendingFn() ([]ResultsDigestData, error) {
	return append([]ResultsDigestData(nil), d.pending...), nil
}
func (d *memDigestStore) add(matchID int64, text string) error {
	d.notes = append(d.notes, text)
	// Mark that match done by dropping it from pending.
	kept := d.pending[:0]
	for _, p := range d.pending {
		if p.MatchID != matchID {
			kept = append(kept, p)
		}
	}
	d.pending = kept
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

func TestCommentWorkerWritesOneNotePerGame(t *testing.T) {
	now := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	store := &memDigestStore{pending: []ResultsDigestData{
		{MatchID: 1, Matches: []FinishedMatchDigest{{TeamA: "Brazil", TeamB: "Spain", Score: "2-1"}}},
	}}

	fc := &fakeCommenter{
		comments: []Comment{{UserID: 1, Player: "Joao", Text: "nice call"}},
		digest:   "Brazil edged Spain 2-1; the story of the night.",
	}
	w := NewCommentWorker(CommentDeps{
		History:        func() ([]RoundStanding, error) { return oneRound(), nil },
		Config:         func() CommentConfig { return CommentConfig{DefaultTone: "playful"} },
		Now:            func() time.Time { return now },
		PendingDigests: store.pendingFn,
		DerivedNotes:   store.load,
		AddDerivedNote: store.add,
	}, fc, NewCommentCache(), NewCommentMonitor("t", time.Hour), "", "")

	// First pass: one pending game ⇒ one digest call, note stored + fed to the writer.
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

	// Second pass with nothing pending ⇒ no new digest call (each game noted once).
	w.pass(context.Background())
	if fc.digestCalls != 1 {
		t.Fatalf("digest re-called with nothing pending: %d", fc.digestCalls)
	}

	// A new finished game enters the queue ⇒ exactly one more note.
	store.pending = append(store.pending, ResultsDigestData{MatchID: 2, Matches: []FinishedMatchDigest{{TeamA: "Argentina", TeamB: "France", Score: "0-0"}}})
	fc.digest = "Argentina and France played out a goalless draw."
	w.pass(context.Background())
	if fc.digestCalls != 2 {
		t.Fatalf("digest calls after new game = %d, want 2", fc.digestCalls)
	}
	if len(store.notes) != 2 {
		t.Fatalf("notes after new game = %d, want 2", len(store.notes))
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

// A logged leaderboard frame round-trips: appendLiveSnapshotLog writes it, and
// RecentLiveSnapshots recovers it (filtering by source + since), while prose
// live_comment lines in the same file are ignored.
func TestLiveSnapshotLogRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ai_comments.log")
	base := time.Date(2026, 6, 20, 9, 0, 0, 0, time.UTC)

	// An older frame (before `since`) and a prose line that must be skipped.
	if err := appendLiveSnapshotLog(path, base.Add(-2*time.Hour), LiveSnapshot{Score: "A 0-0 B", Clock: "1'"}); err != nil {
		t.Fatal(err)
	}
	if err := appendLiveCommentLog(path, base, "prose, not a frame"); err != nil {
		t.Fatal(err)
	}
	f1 := LiveSnapshot{Clock: "30'", Score: "Senegal 1-0 Brazil", Standings: []SnapStanding{{Player: "Helton", Position: 1, Total: 12, LivePoints: 3}, {Player: "Betânia", Position: 2, Total: 11}}}
	f2 := LiveSnapshot{Clock: "70'", Score: "Senegal 2-1 Brazil", Standings: []SnapStanding{{Player: "Betânia", Position: 1, Total: 14}, {Player: "Helton", Position: 2, Total: 12}}}
	if err := appendLiveSnapshotLog(path, base.Add(30*time.Minute), f1); err != nil {
		t.Fatal(err)
	}
	if err := appendLiveSnapshotLog(path, base.Add(70*time.Minute), f2); err != nil {
		t.Fatal(err)
	}

	got := RecentLiveSnapshots(path, base, 10)
	if len(got) != 2 {
		t.Fatalf("want 2 frames since base (old one filtered), got %d: %+v", len(got), got)
	}
	if got[0].Score != "Senegal 1-0 Brazil" || got[1].Score != "Senegal 2-1 Brazil" {
		t.Errorf("frames out of order or wrong: %+v", got)
	}
	if len(got[0].Standings) != 2 || got[1].Standings[0].Player != "Betânia" || got[1].Standings[0].Position != 1 {
		t.Errorf("standings not preserved: %+v", got[1].Standings)
	}
	// Cap keeps the most recent.
	if capped := RecentLiveSnapshots(path, base, 1); len(capped) != 1 || capped[0].Score != "Senegal 2-1 Brazil" {
		t.Errorf("cap=1 gave %+v", capped)
	}
}

// The digest prompt steers BETanIA to recount the leaderboard dance and carries the
// snapshot frames as data the model can ground in.
func TestDigestPromptCarriesDance(t *testing.T) {
	data := ResultsDigestData{
		Matches: []FinishedMatchDigest{{TeamA: "Senegal", TeamB: "Brazil", Score: "2-1"}},
		LiveSnapshots: []LiveSnapshot{
			{Clock: "30'", Score: "Senegal 1-0 Brazil", Standings: []SnapStanding{{Player: "Helton", Position: 1, Total: 12}}},
			{Clock: "70'", Score: "Senegal 2-1 Brazil", Standings: []SnapStanding{{Player: "Betânia", Position: 1, Total: 14}}},
		},
	}
	p := digestPrompt(data, CommentConfig{DefaultTone: "playful"})
	if !strings.Contains(p, "LEADERBOARD DANCE") {
		t.Error("digest prompt must instruct the model to recount the leaderboard dance")
	}
	if !strings.Contains(p, `"live_snapshots"`) || !strings.Contains(p, "Senegal 2-1 Brazil") {
		t.Error("digest prompt must carry the snapshot frames as data")
	}
}

// liveSnapSignature changes only when a live score or a standing position/total
// moves — so a frame is logged once per real shift, not every tick.
func TestLiveSnapSignature(t *testing.T) {
	a := LiveSituation{
		Matches:   []LiveMatchInfo{{TeamA: "A", TeamB: "B", ScoreA: 1, ScoreB: 0, Clock: "30'"}},
		Standings: []LiveStanding{{Player: "X", Position: 1, Total: 10}, {Player: "Y", Position: 2, Total: 8}},
	}
	// Same scores + standings, only the clock advanced ⇒ unchanged signature.
	b := a
	b.Matches = []LiveMatchInfo{{TeamA: "A", TeamB: "B", ScoreA: 1, ScoreB: 0, Clock: "44'"}}
	if liveSnapSignature(a) != liveSnapSignature(b) {
		t.Error("a clock-only change must NOT alter the snapshot signature")
	}
	// A goal flips the standings ⇒ signature changes.
	c := LiveSituation{
		Matches:   []LiveMatchInfo{{TeamA: "A", TeamB: "B", ScoreA: 1, ScoreB: 1, Clock: "60'"}},
		Standings: []LiveStanding{{Player: "Y", Position: 1, Total: 11}, {Player: "X", Position: 2, Total: 10}},
	}
	if liveSnapSignature(a) == liveSnapSignature(c) {
		t.Error("a goal that reorders the standings must change the signature")
	}
}
