package service

import (
	"errors"
	"testing"
	"time"

	"bethoven/internal/models"
	"bethoven/internal/scoring"
)

// TestScoringModeDefaultAndGate checks the default mode and admin gating.
func TestScoringModeDefaultAndGate(t *testing.T) {
	svc, _, _ := newTestService(t)
	player, _ := svc.Register("SHA256:p", testInvite, "Player")
	admin, _ := svc.Register(adminFP, "", "Boss")

	mode, err := svc.ScoringMode()
	if err != nil {
		t.Fatalf("ScoringMode: %v", err)
	}
	if mode != scoring.ModeClassic {
		t.Errorf("default mode = %v, want Classic", mode)
	}

	// Players cannot change the mode.
	if err := svc.SetScoringMode(player, scoring.ModeProximity); !errors.Is(err, ErrForbidden) {
		t.Errorf("player SetScoringMode = %v, want ErrForbidden", err)
	}
	// Admins can.
	if err := svc.SetScoringMode(admin, scoring.ModeProximity); err != nil {
		t.Fatalf("admin SetScoringMode: %v", err)
	}
	if mode, _ := svc.ScoringMode(); mode != scoring.ModeProximity {
		t.Errorf("after set, mode = %v, want Proximity", mode)
	}
}

// TestLeaderboardHonorsMode verifies the leaderboard scores the same bets
// differently under each mode. Setup: a 4-1 result where Alice is the lone
// home-win caller with a close (3-1) guess; seven others miss the result. The
// field is 8 (the Scarcity quorum) so the contrarian bonus is in play.
func TestLeaderboardHonorsMode(t *testing.T) {
	svc, store, fc := newTestService(t)
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")
	carol, _ := svc.Register("SHA256:carol", testInvite, "Carol")
	dave, _ := svc.Register("SHA256:dave", testInvite, "Dave")
	erin, _ := svc.Register("SHA256:erin", testInvite, "Erin")
	frank, _ := svc.Register("SHA256:frank", testInvite, "Frank")
	gina, _ := svc.Register("SHA256:gina", testInvite, "Gina")
	hank, _ := svc.Register("SHA256:hank", testInvite, "Hank")
	admin, _ := svc.Register(adminFP, "", "Boss")

	mid := addMatch(t, store, svc.tournamentID, base.Add(time.Hour))

	// 8 bets on the match (= quorum); only Alice calls the home win, closely (3-1).
	_ = svc.PlaceBet(alice.ID, mid, 3, 1) // home win, 1 goal off
	_ = svc.PlaceBet(bob.ID, mid, 0, 1)   // away win (wrong)
	_ = svc.PlaceBet(carol.ID, mid, 0, 2) // away win (wrong)
	_ = svc.PlaceBet(dave.ID, mid, 1, 1)  // draw (wrong)
	_ = svc.PlaceBet(erin.ID, mid, 2, 2)  // draw (wrong)
	_ = svc.PlaceBet(frank.ID, mid, 0, 3) // away win (wrong)
	_ = svc.PlaceBet(gina.ID, mid, 0, 0)  // draw (wrong)
	_ = svc.PlaceBet(hank.ID, mid, 1, 2)  // away win (wrong)

	fc.T = base.Add(2 * time.Hour)
	if err := svc.EnterResult(admin, mid, 4, 1); err != nil {
		t.Fatal(err)
	}

	// Classic: correct result only -> 1.
	if err := svc.SetScoringMode(admin, scoring.ModeClassic); err != nil {
		t.Fatal(err)
	}
	board, _ := svc.Leaderboard()
	if got := totalFor(board, alice.ID); got != 1 {
		t.Errorf("Classic Alice total = %d, want 1", got)
	}

	// Proximity: max(1, 5-1) = 4.
	if err := svc.SetScoringMode(admin, scoring.ModeProximity); err != nil {
		t.Fatal(err)
	}
	board, _ = svc.Leaderboard()
	if got := totalFor(board, alice.ID); got != 4 {
		t.Errorf("Proximity Alice total = %d, want 4", got)
	}

	// Scarcity: proximity 4 + contrarian result bonus 2 (1 of 8 = 12.5% < 25%,
	// and the field meets the quorum) = 6.
	if err := svc.SetScoringMode(admin, scoring.ModeScarcity); err != nil {
		t.Fatal(err)
	}
	board, _ = svc.Leaderboard()
	if got := totalFor(board, alice.ID); got != 6 {
		t.Errorf("Scarcity Alice total = %d, want 6", got)
	}

	// Whichever mode, the wrong-result bettors stay at 0.
	if got := totalFor(board, bob.ID); got != 0 {
		t.Errorf("Scarcity Bob total = %d, want 0", got)
	}
	if board[0].User.ID != alice.ID {
		t.Errorf("leader = %+v, want Alice", board[0].User)
	}
}

// TestScarcityQuorum proves the fix for tiny fields: with fewer than the quorum
// of bets on a match, the contrarian bonus is suppressed and Scarcity scores
// identically to plain Proximity — so a lone "rare" picker can't vault ahead on
// what is really just statistical noise.
func TestScarcityQuorum(t *testing.T) {
	svc, store, fc := newTestService(t)
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")
	carol, _ := svc.Register("SHA256:carol", testInvite, "Carol")
	dave, _ := svc.Register("SHA256:dave", testInvite, "Dave")
	admin, _ := svc.Register(adminFP, "", "Boss")

	mid := addMatch(t, store, svc.tournamentID, base.Add(time.Hour))

	// Only 4 bets — below the quorum of 8. Alice is the lone home-win caller.
	_ = svc.PlaceBet(alice.ID, mid, 3, 1) // home win, 1 goal off
	_ = svc.PlaceBet(bob.ID, mid, 0, 1)   // away win (wrong)
	_ = svc.PlaceBet(carol.ID, mid, 0, 2) // away win (wrong)
	_ = svc.PlaceBet(dave.ID, mid, 1, 1)  // draw (wrong)

	fc.T = base.Add(2 * time.Hour)
	if err := svc.EnterResult(admin, mid, 4, 1); err != nil {
		t.Fatal(err)
	}

	if err := svc.SetScoringMode(admin, scoring.ModeScarcity); err != nil {
		t.Fatal(err)
	}
	board, _ := svc.Leaderboard()
	// No bonus below quorum: just the Proximity base of 4, not 6.
	if got := totalFor(board, alice.ID); got != 4 {
		t.Errorf("Scarcity (below quorum) Alice total = %d, want 4 (Proximity base, no bonus)", got)
	}
}

// TestRoundWeightsDefaultAndGate checks the default scheme and admin gating.
func TestRoundWeightsDefaultAndGate(t *testing.T) {
	svc, _, _ := newTestService(t)
	player, _ := svc.Register("SHA256:p", testInvite, "Player")
	admin, _ := svc.Register(adminFP, "", "Boss")

	w, err := svc.RoundWeights()
	if err != nil {
		t.Fatalf("RoundWeights: %v", err)
	}
	if w != scoring.WeightFlat {
		t.Errorf("default scheme = %v, want Flat", w)
	}
	if err := svc.SetRoundWeights(player, scoring.WeightDoubling); !errors.Is(err, ErrForbidden) {
		t.Errorf("player SetRoundWeights = %v, want ErrForbidden", err)
	}
	if err := svc.SetRoundWeights(admin, scoring.WeightDoubling); err != nil {
		t.Fatalf("admin SetRoundWeights: %v", err)
	}
	if w, _ := svc.RoundWeights(); w != scoring.WeightDoubling {
		t.Errorf("after set, scheme = %v, want Doubling", w)
	}
}

// TestRoundWeightsScaleKnockouts verifies that, under the doubling scheme, a
// knockout result scores base×phaseWeight while a group result stays at base —
// and that the default (flat) leaves group and knockout identical.
func TestRoundWeightsScaleKnockouts(t *testing.T) {
	svc, store, fc := newTestService(t)
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	admin, _ := svc.Register(adminFP, "", "Boss")

	// A group match and a final, both kicking off later.
	kickoff := base.Add(time.Hour)
	groupID := addMatch(t, store, svc.tournamentID, kickoff)
	finalID, err := store.CreateMatch(models.Match{
		TournamentID: svc.tournamentID, TeamA: "A", TeamB: "B",
		Phase: models.PhaseFinal, StartsAt: kickoff,
	})
	if err != nil {
		t.Fatalf("CreateMatch final: %v", err)
	}

	// Alice nails both exact scores (Classic exact = 3 each).
	if err := svc.PlaceBet(alice.ID, groupID, 2, 1); err != nil {
		t.Fatal(err)
	}
	if err := svc.PlaceBet(alice.ID, finalID, 1, 0); err != nil {
		t.Fatal(err)
	}
	fc.T = kickoff.Add(2 * time.Hour)
	if err := svc.EnterResult(admin, groupID, 2, 1); err != nil {
		t.Fatal(err)
	}
	if err := svc.EnterResult(admin, finalID, 1, 0); err != nil {
		t.Fatal(err)
	}

	// Flat (default): both exacts worth 3 → total 6.
	if board, _ := svc.Leaderboard(); totalFor(board, alice.ID) != 6 {
		t.Errorf("flat total = %d, want 6 (3 group + 3 final)",
			totalFor(boardOf(svc), alice.ID))
	}

	// Doubling: group exact stays 3, final exact ×16 = 48 → total 51.
	if err := svc.SetRoundWeights(admin, scoring.WeightDoubling); err != nil {
		t.Fatal(err)
	}
	if got := totalFor(boardOf(svc), alice.ID); got != 51 {
		t.Errorf("doubling total = %d, want 51 (3 group + 48 final)", got)
	}

	// The final's per-match points reflect the weight too (not just the total).
	rows, _, err := svc.MyResults(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Match.ID == finalID && r.Points != 48 {
			t.Errorf("final row points = %d, want 48", r.Points)
		}
		if r.Match.ID == groupID && r.Points != 3 {
			t.Errorf("group row points = %d, want 3", r.Points)
		}
	}
}

// boardOf is a tiny helper to fetch the leaderboard inline in assertions.
func boardOf(svc *Service) []Standing {
	board, _ := svc.Leaderboard()
	return board
}
