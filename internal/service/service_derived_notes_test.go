package service

import (
	"errors"
	"strings"
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

func TestPendingDigestsOneNotePerGameGoingForward(t *testing.T) {
	svc, store, _ := newTestService(t)
	alice, _ := svc.Register("SHA256:a", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:b", testInvite, "Bob")

	// A game already finished BEFORE the feature's first encounter.
	old := addMatch(t, store, svc.tournamentID, base.Add(-3*time.Hour))
	if err := store.SetResult(old, 1, 0); err != nil {
		t.Fatal(err)
	}

	// First call seeds: adopts the already-finished game as done, returns nothing
	// (no backfill of past games).
	pending, err := svc.PendingDigests()
	if err != nil {
		t.Fatalf("PendingDigests: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("first call should backfill nothing, got %d", len(pending))
	}

	// Now a new game finishes — it becomes pending exactly once.
	played := addMatch(t, store, svc.tournamentID, base.Add(-2*time.Hour))
	if err := store.UpsertBet(models.Bet{UserID: alice.ID, MatchID: played, PredA: 2, PredB: 1}, base); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertBet(models.Bet{UserID: bob.ID, MatchID: played, PredA: 0, PredB: 0}, base); err != nil {
		t.Fatal(err)
	}
	if err := store.SetResult(played, 2, 1); err != nil {
		t.Fatal(err)
	}

	pending, err = svc.PendingDigests()
	if err != nil {
		t.Fatalf("PendingDigests: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("want one pending game, got %d", len(pending))
	}
	data := pending[0]
	if data.MatchID != played {
		t.Errorf("pending MatchID = %d, want %d", data.MatchID, played)
	}
	if len(data.Matches) != 1 || data.Matches[0].Score != "2-1" {
		t.Fatalf("pending match = %+v", data.Matches)
	}
	byName := map[string]int{}
	for _, p := range data.Matches[0].Picks {
		byName[p.Player] = p.Points
	}
	if byName["Alice"] != 3 { // Classic: exact = 3
		t.Errorf("Alice points = %d, want 3", byName["Alice"])
	}

	// Once noted, it's no longer pending (one note per game).
	if err := svc.AddDerivedNote(data.MatchID, "Alice nailed the 2-1."); err != nil {
		t.Fatal(err)
	}
	pending, _ = svc.PendingDigests()
	if len(pending) != 0 {
		t.Fatalf("noted game should not be pending again, got %d", len(pending))
	}
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

	// Worker appends three per-game notes (one per match id).
	for i, txt := range []string{"story one", "story two", "story three"} {
		if err := svc.AddDerivedNote(int64(i+1), txt); err != nil {
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

	// Combined feed carries all three, each tagged with the date it was played and
	// led by today's date — so BETanIA can't retell an old game as if it just
	// happened.
	combined := svc.DerivedNotesText()
	if combined == "" {
		t.Fatalf("combined empty")
	}
	if !strings.Contains(combined, "Today is Jun 11.") {
		t.Errorf("combined feed should lead with today's date, got:\n%s", combined)
	}
	if !strings.Contains(combined, "[Jun 11] story one") {
		t.Errorf("combined feed should date-tag each note, got:\n%s", combined)
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
