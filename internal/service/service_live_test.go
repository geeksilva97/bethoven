package service

import (
	"testing"
	"time"

	"bethoven/internal/live"
)

// liveSnap is a fixed LiveStore for tests — no poller, no network.
type liveSnap map[int64]live.Score

func (l liveSnap) Snapshot() map[int64]live.Score { return map[int64]live.Score(l) }

// TestLeaderboardFoldsPartials checks that the leaderboard adds provisional
// points for an in-play match (scored against the live score) on top of the
// authoritative points from a finished match.
func TestLeaderboardFoldsPartials(t *testing.T) {
	svc, store, fc := newTestService(t)
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")
	admin, _ := svc.Register(adminFP, "", "Boss")

	finished := addMatch(t, store, svc.tournamentID, base.Add(time.Hour))
	livem := addMatch(t, store, svc.tournamentID, base.Add(2*time.Hour))

	// Finished match 2-1: Alice exact (3), Bob result-only (1).
	_ = svc.PlaceBet(alice.ID, finished, 2, 1)
	_ = svc.PlaceBet(bob.ID, finished, 3, 0)
	// In-play match, currently 0-0: Alice bet 0-0 (would-be exact, provisional 3),
	// Bob bet 1-0 (wrong result so far, provisional 0).
	_ = svc.PlaceBet(alice.ID, livem, 0, 0)
	_ = svc.PlaceBet(bob.ID, livem, 1, 0)

	fc.T = base.Add(90 * time.Minute)
	if err := svc.EnterResult(admin, finished, 2, 1); err != nil {
		t.Fatal(err)
	}

	// No live store yet: only the finished match counts.
	board, _ := svc.Leaderboard()
	if got := totalFor(board, alice.ID); got != 3 {
		t.Fatalf("pre-live Alice total = %d, want 3", got)
	}

	// Attach a live snapshot: match in play at 0-0.
	svc.SetLiveStore(liveSnap{livem: {A: 0, B: 0, State: live.StateIn, Clock: "30'"}})
	board, _ = svc.Leaderboard()

	if got, lp := totalFor(board, alice.ID), liveFor(board, alice.ID); got != 6 || lp != 3 {
		t.Errorf("Alice total/live = %d/%d, want 6/3 (3 final + 3 provisional)", got, lp)
	}
	if got, lp := totalFor(board, bob.ID), liveFor(board, bob.ID); got != 1 || lp != 0 {
		t.Errorf("Bob total/live = %d/%d, want 1/0", got, lp)
	}
	// Standings sort by total desc: Alice(6), Bob(1), admin(0).
	if board[0].User.ID != alice.ID {
		t.Errorf("leader = %+v, want Alice", board[0])
	}
}

// TestOverlayLiveOnlyInPlay verifies live fields are populated only for in-play
// matches, and never for finished or pre-match ones.
func TestOverlayLiveOnlyInPlay(t *testing.T) {
	svc, store, _ := newTestService(t)
	user, _ := svc.Register("SHA256:u", testInvite, "U")
	inPlay := addMatch(t, store, svc.tournamentID, base.Add(time.Hour))
	pre := addMatch(t, store, svc.tournamentID, base.Add(3*time.Hour))
	_ = user

	svc.SetLiveStore(liveSnap{
		inPlay: {A: 2, B: 1, State: live.StateIn, Clock: "67'"},
		pre:    {A: 0, B: 0, State: live.StatePre},
	})

	fx, err := svc.Fixtures()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range fx {
		switch m.ID {
		case inPlay:
			if !m.Live || m.LiveScoreA != 2 || m.LiveScoreB != 1 || m.LiveClock != "67'" {
				t.Errorf("in-play overlay wrong: %+v", m)
			}
		case pre:
			if m.Live {
				t.Errorf("pre-match must not be marked live: %+v", m)
			}
		}
	}

	lm, err := svc.LiveMatches()
	if err != nil {
		t.Fatal(err)
	}
	if len(lm) != 1 || lm[0].ID != inPlay {
		t.Errorf("LiveMatches = %+v, want only the in-play match", lm)
	}
}

// TestFinalizeFromFeedRespectsAdmin checks the feed records a result, but never
// clobbers an already-finished (admin-set) result.
func TestFinalizeFromFeedRespectsAdmin(t *testing.T) {
	svc, store, fc := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")
	mid := addMatch(t, store, svc.tournamentID, base.Add(time.Hour))
	fc.T = base.Add(2 * time.Hour)

	// Feed finalizes 1-0.
	if err := svc.FinalizeFromFeed(mid, 1, 0); err != nil {
		t.Fatalf("FinalizeFromFeed: %v", err)
	}
	m, _ := store.MatchByID(mid)
	if !m.Finished || *m.ScoreA != 1 || *m.ScoreB != 0 {
		t.Fatalf("feed result not recorded: %+v", m)
	}

	// Admin overrides to 2-2.
	if err := svc.EnterResult(admin, mid, 2, 2); err != nil {
		t.Fatal(err)
	}
	// A later feed poll (e.g. a stale 1-0) must NOT overwrite the admin result.
	if err := svc.FinalizeFromFeed(mid, 1, 0); err != nil {
		t.Fatal(err)
	}
	m, _ = store.MatchByID(mid)
	if *m.ScoreA != 2 || *m.ScoreB != 2 {
		t.Errorf("feed clobbered admin override: %+v", m)
	}
}

// TestLeaderboardLiveRankDelta checks that in-play points produce the right rank
// movement: the climber gets a positive delta, the player they overtook a negative
// one, and an uninvolved player zero. With no live store, all deltas are 0.
func TestLeaderboardLiveRankDelta(t *testing.T) {
	svc, store, fc := newTestService(t)
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")
	admin, _ := svc.Register(adminFP, "", "Boss") // bets nothing — uninvolved

	finished := addMatch(t, store, svc.tournamentID, base.Add(time.Hour))
	livem := addMatch(t, store, svc.tournamentID, base.Add(2*time.Hour))

	// Finished 2-1: Bob exact (3), Alice result-only (1) — settled, Bob leads Alice.
	_ = svc.PlaceBet(alice.ID, finished, 2, 0)
	_ = svc.PlaceBet(bob.ID, finished, 2, 1)
	// In-play, currently 1-0: Alice bet 1-0 (provisional exact 3), Bob bet 0-2 (0).
	_ = svc.PlaceBet(alice.ID, livem, 1, 0)
	_ = svc.PlaceBet(bob.ID, livem, 0, 2)

	fc.T = base.Add(90 * time.Minute)
	if err := svc.EnterResult(admin, finished, 2, 1); err != nil {
		t.Fatal(err)
	}

	// No live store: settled order, no movement.
	board, _ := svc.Leaderboard()
	for _, s := range board {
		if s.LiveRankDelta != 0 {
			t.Errorf("pre-live delta for %s = %d, want 0", s.User.DisplayName, s.LiveRankDelta)
		}
	}

	// Live match in play: Alice (1+3=4) overtakes Bob (3+0=3).
	svc.SetLiveStore(liveSnap{livem: {A: 1, B: 0, State: live.StateIn, Clock: "30'"}})
	board, _ = svc.Leaderboard()

	if got := deltaFor(board, alice.ID); got <= 0 {
		t.Errorf("Alice delta = %d, want positive (climbed)", got)
	}
	if got := deltaFor(board, bob.ID); got >= 0 {
		t.Errorf("Bob delta = %d, want negative (dropped)", got)
	}
	if got := deltaFor(board, admin.ID); got != 0 {
		t.Errorf("Boss (uninvolved) delta = %d, want 0", got)
	}
	if board[0].User.ID != alice.ID {
		t.Errorf("leader = %+v, want Alice", board[0])
	}
}

func deltaFor(board []Standing, userID int64) int {
	for _, s := range board {
		if s.User.ID == userID {
			return s.LiveRankDelta
		}
	}
	return 0
}

func totalFor(board []Standing, userID int64) int {
	for _, s := range board {
		if s.User.ID == userID {
			return s.Total
		}
	}
	return -1
}

func liveFor(board []Standing, userID int64) int {
	for _, s := range board {
		if s.User.ID == userID {
			return s.LivePoints
		}
	}
	return -1
}
