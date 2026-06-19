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

	// Alice nails the exact score (2-1) -> 3. Bob gets the result only
	// (home win, wrong scoreline) -> 1.
	if err := svc.PlaceBet(alice.ID, mid, 2, 1); err != nil {
		t.Fatal(err)
	}
	if err := svc.PlaceBet(bob.ID, mid, 3, 0); err != nil {
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
	if total != 3 {
		t.Errorf("Alice total = %d, want 3", total)
	}
	if len(rows) != 1 || rows[0].Points != 3 {
		t.Errorf("Alice row points = %+v, want 3", rows)
	}

	// Leaderboard: Alice (3) ahead of Bob (1); admin (0) last.
	board, err := svc.Leaderboard()
	if err != nil {
		t.Fatal(err)
	}
	if len(board) != 3 {
		t.Fatalf("expected 3 standings, got %d", len(board))
	}
	if board[0].User.ID != alice.ID || board[0].Total != 3 {
		t.Errorf("leader = %+v, want Alice/3", board[0])
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

	// Bob exact (3), Alice result-only (1). Admin didn't bet.
	_ = svc.PlaceBet(bob.ID, mid, 2, 1)
	_ = svc.PlaceBet(alice.ID, mid, 3, 0)
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
	if rows[0].User.ID != bob.ID || rows[0].Points != 3 {
		t.Errorf("game winner = %+v, want Bob/3", rows[0])
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

	_ = svc.PlaceBet(alice.ID, mid, 2, 1)
	_ = svc.PlaceBet(bob.ID, mid, 0, 0)

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

	if err := svc.PlaceBet(alice.ID, mid, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := svc.PlaceBet(bob.ID, mid, 0, 3); err != nil {
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

	_ = svc.PlaceBet(alice.ID, m1, 1, 0) // Alice bets m1 only
	_ = svc.PlaceBet(bob.ID, m1, 3, 3)   // Bob bets m1
	_ = svc.PlaceBet(bob.ID, m2, 0, 0)   // Bob bets m2

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
	_ = svc.PlaceBet(player.ID, mid, 1, 1)

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

// TestBetterRank exercises the shared leaderboard comparator directly: points
// dominate, then exact hits, then correct results, then alphabetical name.
func TestBetterRank(t *testing.T) {
	cases := []struct {
		name string
		a, b [3]int // points, exact, results
		na   string
		nb   string
		want bool // true => a sorts above b
	}{
		{"more points wins", [3]int{5, 0, 0}, [3]int{4, 9, 9}, "Z", "A", true},
		{"exact breaks point tie", [3]int{3, 1, 1}, [3]int{3, 0, 3}, "Z", "A", true},
		{"results break exact tie", [3]int{3, 0, 3}, [3]int{3, 0, 1}, "Z", "A", true},
		{"name breaks full tie", [3]int{3, 1, 1}, [3]int{3, 1, 1}, "Amy", "Bea", true},
		{"name fallback loser", [3]int{3, 1, 1}, [3]int{3, 1, 1}, "Bea", "Amy", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := betterRank(tc.a[0], tc.a[1], tc.a[2], tc.na, tc.b[0], tc.b[1], tc.b[2], tc.nb)
			if got != tc.want {
				t.Errorf("betterRank(%v %q, %v %q) = %v, want %v", tc.a, tc.na, tc.b, tc.nb, got, tc.want)
			}
		})
	}
}

// TestLeaderboardTiebreak proves the comparator is wired into Leaderboard end to
// end: among players tied on total points, more exact scores ranks higher (beating
// both alphabetical order and a larger correct-result count), and a full tie falls
// back to alphabetical name.
func TestLeaderboardTiebreak(t *testing.T) {
	svc, store, fc := newTestService(t)
	// Amy: 3 correct-results, 0 exact. Bea/Zoe: 1 exact each, identical otherwise.
	amy, _ := svc.Register("SHA256:amy", testInvite, "Amy")
	bea, _ := svc.Register("SHA256:bea", testInvite, "Bea")
	zoe, _ := svc.Register("SHA256:zoe", testInvite, "Zoe")
	admin, _ := svc.Register(adminFP, "", "Boss")

	kickoff := base.Add(time.Hour)
	m1 := addMatch(t, store, svc.tournamentID, kickoff)
	m2 := addMatch(t, store, svc.tournamentID, kickoff)
	m3 := addMatch(t, store, svc.tournamentID, kickoff)

	// All three matches finish 2-1 (home win).
	// Amy: result-only on all three (1+1+1 = 3 total, 0 exact, 3 results).
	mustBet(t, svc, amy.ID, m1, 1, 0)
	mustBet(t, svc, amy.ID, m2, 3, 0)
	mustBet(t, svc, amy.ID, m3, 1, 0)
	// Bea & Zoe: exact on m1, misses on m2/m3 (3 total, 1 exact, 1 result).
	for _, u := range []int64{bea.ID, zoe.ID} {
		mustBet(t, svc, u, m1, 2, 1)
		mustBet(t, svc, u, m2, 0, 2)
		mustBet(t, svc, u, m3, 0, 3)
	}

	fc.T = kickoff.Add(2 * time.Hour)
	for _, mid := range []int64{m1, m2, m3} {
		if err := svc.EnterResult(admin, mid, 2, 1); err != nil {
			t.Fatalf("EnterResult: %v", err)
		}
	}

	board, err := svc.Leaderboard()
	if err != nil {
		t.Fatal(err)
	}
	// All of Amy/Bea/Zoe have 3 points. Expected order: Bea, Zoe (1 exact, alpha),
	// then Amy (0 exact), then Boss (0 points).
	got := []int64{board[0].User.ID, board[1].User.ID, board[2].User.ID, board[3].User.ID}
	want := []int64{bea.ID, zoe.ID, amy.ID, admin.ID}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("leaderboard order = %v, want %v (Bea>Zoe by name, both > Amy by exact hits)", got, want)
		}
	}
	// Sanity: the tied trio really are tied on points, and exact counts differ.
	if board[0].Total != 3 || board[2].Total != 3 {
		t.Errorf("expected the top three tied on 3 points, got %d/%d", board[0].Total, board[2].Total)
	}
	if board[0].ExactHits != 1 || board[2].ExactHits != 0 {
		t.Errorf("expected Bea 1 exact / Amy 0 exact, got %d/%d", board[0].ExactHits, board[2].ExactHits)
	}
}

// mustBet places a bet and fails the test on error.
func mustBet(t *testing.T, svc *Service, uid, mid, a, b int64) {
	t.Helper()
	if err := svc.PlaceBet(uid, mid, a, b); err != nil {
		t.Fatalf("PlaceBet(u=%d m=%d %d-%d): %v", uid, mid, a, b, err)
	}
}
