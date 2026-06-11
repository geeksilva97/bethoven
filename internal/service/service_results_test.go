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
