package service

import (
	"errors"
	"testing"
	"time"

	"bethoven/internal/models"
)

// TestScoringEndToEnd places bets, has an admin enter a result, and checks the
// per-player results and leaderboard totals match the scoring spec.
func TestScoringEndToEnd(t *testing.T) {
	svc, store, fc := newTestService(t)
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")
	admin, _ := svc.Register(adminFP, "", "Boss")

	kickoff := base.Add(time.Hour)
	mid := addMatch(t, store, svc.tournamentID, kickoff)

	// Alice nails the exact score (2-1, over 2.5) -> 4. Bob gets the result
	// only (home win) with wrong bonus -> 1.
	if err := svc.PlaceBet(alice.ID, mid, 2, 1, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.PlaceBet(bob.ID, mid, 3, 0, false); err != nil {
		t.Fatal(err)
	}

	// Lock and record the actual result 2-1.
	fc.T = kickoff.Add(2 * time.Hour)
	if err := svc.EnterResult(admin, mid, 2, 1); err != nil {
		t.Fatalf("EnterResult: %v", err)
	}

	// Alice's results.
	rows, total, err := svc.MyResults(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 {
		t.Errorf("Alice total = %d, want 4", total)
	}
	if len(rows) != 1 || rows[0].Points != 4 {
		t.Errorf("Alice row points = %+v, want 4", rows)
	}

	// Leaderboard: Alice (4) ahead of Bob (1); admin (0) last.
	board, err := svc.Leaderboard()
	if err != nil {
		t.Fatal(err)
	}
	if len(board) != 3 {
		t.Fatalf("expected 3 standings, got %d", len(board))
	}
	if board[0].User.ID != alice.ID || board[0].Total != 4 {
		t.Errorf("leader = %+v, want Alice/4", board[0])
	}
	if board[1].User.ID != bob.ID || board[1].Total != 1 {
		t.Errorf("second = %+v, want Bob/1", board[1])
	}
}

// TestMatchLeaderboard verifies the per-game ranking orders players by the
// points they earned on that single match.
func TestMatchLeaderboard(t *testing.T) {
	svc, store, fc := newTestService(t)
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")
	admin, _ := svc.Register(adminFP, "", "Boss")
	kickoff := base.Add(time.Hour)
	mid := addMatch(t, store, svc.tournamentID, kickoff)

	// Bob exact (4), Alice result-only (1). Admin didn't bet.
	_ = svc.PlaceBet(bob.ID, mid, 2, 1, true)
	_ = svc.PlaceBet(alice.ID, mid, 3, 0, false)
	fc.T = kickoff.Add(time.Hour)
	if err := svc.EnterResult(admin, mid, 2, 1); err != nil {
		t.Fatal(err)
	}

	m, rows, err := svc.MatchLeaderboard(mid)
	if err != nil {
		t.Fatalf("MatchLeaderboard: %v", err)
	}
	if m.ID != mid {
		t.Errorf("wrong match returned: %d", m.ID)
	}
	if len(rows) != 2 { // only bettors appear
		t.Fatalf("expected 2 rows (bettors only), got %d", len(rows))
	}
	if rows[0].User.ID != bob.ID || rows[0].Points != 4 {
		t.Errorf("game winner = %+v, want Bob/4", rows[0])
	}
	if rows[1].User.ID != alice.ID || rows[1].Points != 1 {
		t.Errorf("runner-up = %+v, want Alice/1", rows[1])
	}
}

// TestMatchLeaderboardHidesPicksUntilFinished verifies the service refuses to
// return any individual picks for a match that has no result yet — the
// disclosure gate lives in the service, not just the TUI.
func TestMatchLeaderboardHidesPicksUntilFinished(t *testing.T) {
	svc, store, fc := newTestService(t)
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")
	admin, _ := svc.Register(adminFP, "", "Boss")
	kickoff := base.Add(time.Hour)
	mid := addMatch(t, store, svc.tournamentID, kickoff)

	_ = svc.PlaceBet(alice.ID, mid, 2, 1, true)
	_ = svc.PlaceBet(bob.ID, mid, 0, 0, false)

	// Before any result exists, NO rows (no picks) may come back.
	m, rows, err := svc.MatchLeaderboard(mid)
	if err != nil {
		t.Fatalf("MatchLeaderboard (unfinished): %v", err)
	}
	if m.ID != mid {
		t.Errorf("wrong match: %d", m.ID)
	}
	if len(rows) != 0 {
		t.Fatalf("unfinished match must expose 0 picks, got %d rows: %+v", len(rows), rows)
	}

	// After a result, picks are revealed and ranked.
	fc.T = kickoff.Add(time.Hour)
	if err := svc.EnterResult(admin, mid, 2, 1); err != nil {
		t.Fatal(err)
	}
	_, rows, err = svc.MatchLeaderboard(mid)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("finished match should reveal both bettors, got %d", len(rows))
	}
}

// TestMyResultsIsolation verifies a player's results contain only their own
// bets, never another player's picks.
func TestMyResultsIsolation(t *testing.T) {
	svc, store, _ := newTestService(t)
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")
	mid := addMatch(t, store, svc.tournamentID, base.Add(time.Hour))

	if err := svc.PlaceBet(alice.ID, mid, 1, 0, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.PlaceBet(bob.ID, mid, 0, 3, true); err != nil {
		t.Fatal(err)
	}

	rows, _, err := svc.MyResults(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Bet == nil {
		t.Fatalf("expected Alice's one bet, got %+v", rows)
	}
	if rows[0].Bet.UserID != alice.ID || rows[0].Bet.PredB != 0 {
		t.Errorf("Alice's results leaked another user's bet: %+v", rows[0].Bet)
	}
}

// TestMyResultsMultiMatchNoLeak verifies that across several matches, a player
// sees a row per match with their own bet (or nil where they didn't bet), and
// never another player's pick.
func TestMyResultsMultiMatchNoLeak(t *testing.T) {
	svc, store, _ := newTestService(t)
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")
	m1 := addMatch(t, store, svc.tournamentID, base.Add(time.Hour))
	m2 := addMatch(t, store, svc.tournamentID, base.Add(2*time.Hour))

	_ = svc.PlaceBet(alice.ID, m1, 1, 0, false) // Alice bets m1 only
	_ = svc.PlaceBet(bob.ID, m1, 3, 3, true)    // Bob bets m1
	_ = svc.PlaceBet(bob.ID, m2, 0, 0, false)   // Bob bets m2

	rows, _, err := svc.MyResults(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected a row per match (2), got %d", len(rows))
	}
	for _, r := range rows {
		switch r.Match.ID {
		case m1:
			if r.Bet == nil || r.Bet.UserID != alice.ID || r.Bet.PredA != 1 {
				t.Errorf("m1 should show Alice's own bet, got %+v", r.Bet)
			}
		case m2:
			if r.Bet != nil {
				t.Errorf("m2 should be un-bet for Alice (nil), got %+v", r.Bet)
			}
		}
	}
}

// TestEnterResultValidation checks admin result entry rejects bad scores and
// unknown matches, and that a nil requester is forbidden.
func TestEnterResultValidation(t *testing.T) {
	svc, store, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")
	mid := addMatch(t, store, svc.tournamentID, base.Add(time.Hour))

	if err := svc.EnterResult(admin, mid, -1, 0); !errors.Is(err, ErrInvalidScore) {
		t.Errorf("negative score should be rejected, got %v", err)
	}
	if err := svc.EnterResult(admin, mid, 0, 100); !errors.Is(err, ErrInvalidScore) {
		t.Errorf("out-of-range score should be rejected, got %v", err)
	}
	if err := svc.EnterResult(admin, 9999, 1, 0); !errors.Is(err, ErrMatchNotFound) {
		t.Errorf("unknown match should be ErrMatchNotFound, got %v", err)
	}
	if err := svc.EnterResult(nil, mid, 1, 0); !errors.Is(err, ErrForbidden) {
		t.Errorf("nil requester should be forbidden, got %v", err)
	}
}

// TestAllBetsRequiresAdmin verifies the all-bets grid is admin-gated in the
// service itself — a player cannot reach it.
func TestAllBetsRequiresAdmin(t *testing.T) {
	svc, store, _ := newTestService(t)
	player, _ := svc.Register("SHA256:p", testInvite, "Player")
	admin, _ := svc.Register(adminFP, "", "Boss")
	mid := addMatch(t, store, svc.tournamentID, base.Add(time.Hour))
	_ = svc.PlaceBet(player.ID, mid, 1, 1, false)

	if _, err := svc.AllBets(player); !errors.Is(err, ErrForbidden) {
		t.Errorf("player must not access all-bets grid, got %v", err)
	}
	grid, err := svc.AllBets(admin)
	if err != nil {
		t.Fatalf("admin AllBets: %v", err)
	}
	if cell, ok := grid.Cells[mid][player.ID]; !ok || cell.Bet == nil {
		t.Errorf("admin grid missing player's bet: %+v", grid.Cells)
	}
}

// TestAdminActionsRequireAdmin checks AddMatch and EnterResult reject players.
func TestAdminActionsRequireAdmin(t *testing.T) {
	svc, _, _ := newTestService(t)
	player, _ := svc.Register("SHA256:p", testInvite, "Player")

	if _, err := svc.AddMatch(player, "X", "Y", models.PhaseRound16, "", base); !errors.Is(err, ErrForbidden) {
		t.Errorf("player AddMatch should be forbidden, got %v", err)
	}
	if err := svc.EnterResult(player, 1, 1, 0); !errors.Is(err, ErrForbidden) {
		t.Errorf("player EnterResult should be forbidden, got %v", err)
	}
}
