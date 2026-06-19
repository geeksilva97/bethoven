package service

import (
	"errors"
	"testing"
	"time"

	"bethoven/internal/models"
)

// TestMatchSettleTriggersComments checks that settling a match (admin or feed)
// nudges the comment worker, but a no-op feed write (already finished) does not.
func TestMatchSettleTriggersComments(t *testing.T) {
	svc, store, fc := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")

	var fired int
	svc.SetCommentTrigger(func() bool { fired++; return true })

	m1 := addMatch(t, store, svc.tournamentID, base.Add(-2*time.Hour))
	m2 := addMatch(t, store, svc.tournamentID, base.Add(-1*time.Hour))

	if err := svc.EnterResult(admin, m1, 2, 1); err != nil {
		t.Fatalf("EnterResult: %v", err)
	}
	if fired != 1 {
		t.Fatalf("EnterResult should trigger once, got %d", fired)
	}

	// Feed settles a fresh match -> trigger.
	if err := svc.FinalizeFromFeed(m2, 0, 0); err != nil {
		t.Fatalf("FinalizeFromFeed: %v", err)
	}
	if fired != 2 {
		t.Fatalf("feed settle should trigger, got %d", fired)
	}

	// Feed re-finalizing an already-settled match -> no trigger.
	if err := svc.FinalizeFromFeed(m2, 0, 0); err != nil {
		t.Fatalf("FinalizeFromFeed (noop): %v", err)
	}
	if fired != 2 {
		t.Fatalf("no-op feed write should not trigger, got %d", fired)
	}

	_ = fc
}

func TestResultsDigestData(t *testing.T) {
	svc, store, _ := newTestService(t)
	alice, _ := svc.Register("SHA256:a", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:b", testInvite, "Bob")

	played := addMatch(t, store, svc.tournamentID, base.Add(-2*time.Hour))
	upcoming := addMatch(t, store, svc.tournamentID, base.Add(48*time.Hour))

	// Bets on the played match (before its kickoff, so the lock allows them — the
	// fake clock is at base; played started 2h ago, so place via the store).
	if err := store.UpsertBet(models.Bet{UserID: alice.ID, MatchID: played, PredA: 2, PredB: 1}, base); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertBet(models.Bet{UserID: bob.ID, MatchID: played, PredA: 0, PredB: 0}, base); err != nil {
		t.Fatal(err)
	}
	if err := store.SetResult(played, 2, 1); err != nil {
		t.Fatal(err)
	}

	data, err := svc.ResultsDigestData()
	if err != nil {
		t.Fatalf("ResultsDigestData: %v", err)
	}
	if len(data.Matches) != 1 {
		t.Fatalf("want only the finished match, got %d", len(data.Matches))
	}
	fm := data.Matches[0]
	if fm.Score != "2-1" {
		t.Errorf("score = %q, want 2-1", fm.Score)
	}
	if len(fm.Picks) != 2 {
		t.Fatalf("want 2 picks, got %d", len(fm.Picks))
	}
	// Alice nailed it (Classic: exact = 3); Bob missed.
	byName := map[string]int{}
	for _, p := range fm.Picks {
		byName[p.Player] = p.Points
	}
	if byName["Alice"] != 3 {
		t.Errorf("Alice points = %d, want 3", byName["Alice"])
	}
	_ = upcoming
}

func TestDerivedNotesCurate(t *testing.T) {
	svc, _, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")
	player, _ := svc.Register("SHA256:p", testInvite, "Pleb")

	// Player can't view or curate.
	if _, err := svc.DerivedNotes(player); !errors.Is(err, ErrForbidden) {
		t.Fatalf("player DerivedNotes: want ErrForbidden, got %v", err)
	}
	if err := svc.DeleteDerivedNote(player, 0); !errors.Is(err, ErrForbidden) {
		t.Fatalf("player DeleteDerivedNote: want ErrForbidden, got %v", err)
	}

	// Worker appends three notes.
	for _, txt := range []string{"story one", "story two", "story three"} {
		if err := svc.AddDerivedNote(txt, "sig-"+txt); err != nil {
			t.Fatalf("AddDerivedNote: %v", err)
		}
	}
	notes, err := svc.DerivedNotes(admin)
	if err != nil {
		t.Fatalf("DerivedNotes: %v", err)
	}
	if len(notes) != 3 || notes[0].Text != "story one" {
		t.Fatalf("notes = %+v", notes)
	}

	// Combined feed carries all three, newest sig.
	combined, sig := svc.DerivedNotesText()
	if combined == "" || sig != "sig-story three" {
		t.Fatalf("combined=%q sig=%q", combined, sig)
	}

	// Delete the middle one.
	if err := svc.DeleteDerivedNote(admin, 1); err != nil {
		t.Fatalf("DeleteDerivedNote: %v", err)
	}
	notes, _ = svc.DerivedNotes(admin)
	if len(notes) != 2 || notes[1].Text != "story three" {
		t.Fatalf("after delete: %+v", notes)
	}

	// Compact collapses to the most recent.
	if err := svc.CompactDerivedNotes(admin); err != nil {
		t.Fatalf("CompactDerivedNotes: %v", err)
	}
	notes, _ = svc.DerivedNotes(admin)
	if len(notes) != 1 || notes[0].Text != "story three" {
		t.Fatalf("after compact: %+v", notes)
	}

	// Clear empties it.
	if err := svc.ClearDerivedNotes(admin); err != nil {
		t.Fatalf("ClearDerivedNotes: %v", err)
	}
	if notes, _ = svc.DerivedNotes(admin); len(notes) != 0 {
		t.Fatalf("after clear: %+v", notes)
	}
}
