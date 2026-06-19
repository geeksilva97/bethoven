package service

import (
	"testing"
	"time"

	"bethoven/internal/ai"
)

// betOK places a bet that must succeed (helper for the history test).
func betOK(t *testing.T, svc *Service, uid, mid int64, a, b int) {
	t.Helper()
	if err := svc.PlaceBet(uid, mid, int64(a), int64(b)); err != nil {
		t.Fatalf("PlaceBet u=%d m=%d: %v", uid, mid, err)
	}
}

// psByName returns the standing for a named player in a round (fails if absent).
func psByName(t *testing.T, r ai.RoundStanding, name string) ai.PlayerStanding {
	t.Helper()
	for _, p := range r.Ranks {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("player %q not in round %q", name, r.Label)
	return ai.PlayerStanding{}
}

// TestStandingsHistoryMovements reconstructs a two-round history from finished
// matches alone (no stored standings) and checks positions + per-round movement
// and points-gained deltas — the grounded numbers BETanIA's narrative layer needs.
func TestStandingsHistoryMovements(t *testing.T) {
	svc, store, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, testInvite, "Admin")
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")

	day1 := base.Add(2 * time.Hour)  // 2026-06-11
	day2 := base.Add(26 * time.Hour) // 2026-06-12
	m1 := addMatch(t, store, svc.tournamentID, day1)
	m2 := addMatch(t, store, svc.tournamentID, day2)

	// All bets are placed before any kickoff (clock = base).
	betOK(t, svc, alice.ID, m1, 2, 1) // will be exact
	betOK(t, svc, bob.ID, m1, 1, 0)   // right result only
	betOK(t, svc, alice.ID, m2, 1, 1) // wrong
	betOK(t, svc, bob.ID, m2, 0, 3)   // will be exact

	// Results (admin; not time-gated). Classic scoring: exact=3, result=1, miss=0.
	if err := svc.EnterResult(admin, m1, 2, 1); err != nil {
		t.Fatalf("EnterResult m1: %v", err)
	}
	if err := svc.EnterResult(admin, m2, 0, 3); err != nil {
		t.Fatalf("EnterResult m2: %v", err)
	}

	hist, err := svc.StandingsHistory()
	if err != nil {
		t.Fatalf("StandingsHistory: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("expected 2 rounds, got %d", len(hist))
	}

	// Round 1 (Jun 11): Alice 3 (#1), Bob 1 (#2); first round ⇒ movement 0.
	if a := psByName(t, hist[0], "Alice"); a.Position != 1 || a.Total != 3 || a.Movement != 0 || a.PointsGained != 3 {
		t.Errorf("R1 Alice = %+v", a)
	}
	if b := psByName(t, hist[0], "Bob"); b.Position != 2 || b.Total != 1 || b.Movement != 0 || b.PointsGained != 1 {
		t.Errorf("R1 Bob = %+v", b)
	}

	// Round 2 (Jun 12): Bob 4 (#1, climbed +1, +3), Alice 3 (#2, fell -1, +0).
	if b := psByName(t, hist[1], "Bob"); b.Position != 1 || b.Total != 4 || b.Movement != 1 || b.PointsGained != 3 {
		t.Errorf("R2 Bob = %+v", b)
	}
	if a := psByName(t, hist[1], "Alice"); a.Position != 2 || a.Total != 3 || a.Movement != -1 || a.PointsGained != 0 {
		t.Errorf("R2 Alice = %+v", a)
	}
}

type fakeCommentSource struct{ m map[int64]ai.Comment }

func (f fakeCommentSource) All(now time.Time) map[int64]ai.Comment { return f.m }

// TestLeaderboardCommentsScoping asserts the visibility boundary: a player sees
// only their own comment, an admin sees everyone's, and no source ⇒ none.
func TestLeaderboardCommentsScoping(t *testing.T) {
	svc, _, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, testInvite, "Admin")
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")

	if got := svc.LeaderboardComments(alice); len(got) != 0 {
		t.Fatalf("no source should yield no comments, got %v", got)
	}

	svc.SetCommentSource(fakeCommentSource{m: map[int64]ai.Comment{
		alice.ID: {UserID: alice.ID, Text: "alice line"},
		bob.ID:   {UserID: bob.ID, Text: "bob line"},
	}})

	if got := svc.LeaderboardComments(alice); len(got) != 1 || got[alice.ID] != "alice line" {
		t.Fatalf("player should see only own comment, got %v", got)
	}
	if got := svc.LeaderboardComments(admin); len(got) != 2 || got[alice.ID] == "" || got[bob.ID] == "" {
		t.Fatalf("admin should see all comments, got %v", got)
	}
}
