package service

import (
	"errors"
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

// TestCommentConfigTonesAndContext checks the worker config the service builds:
// default tone, per-player overrides (incl. mute), and resolved rivalries/notes —
// plus that "default" clears an override and deletes drop entries.
func TestCommentConfigTonesAndContext(t *testing.T) {
	svc, _, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, testInvite, "Admin")
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")

	if err := svc.SetCommentTone(admin, "savage"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetUserCommentTone(admin, alice.ID, "playful"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetUserCommentTone(admin, bob.ID, "mute"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddRivalry(admin, alice.ID, bob.ID, "office rivals"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddCommentNote(admin, "loser buys lunch"); err != nil {
		t.Fatal(err)
	}

	cfg := svc.CommentConfig()
	if cfg.DefaultTone != "savage" {
		t.Errorf("default tone = %q", cfg.DefaultTone)
	}
	if cfg.ToneByName["Alice"] != "playful" || cfg.ToneByName["Bob"] != "mute" {
		t.Errorf("tone overrides = %v", cfg.ToneByName)
	}
	if len(cfg.Rivalries) != 1 || cfg.Rivalries[0].A != "Alice" || cfg.Rivalries[0].B != "Bob" || cfg.Rivalries[0].Note != "office rivals" {
		t.Errorf("rivalries = %+v", cfg.Rivalries)
	}
	if len(cfg.Notes) != 1 || cfg.Notes[0] != "loser buys lunch" {
		t.Errorf("notes = %+v", cfg.Notes)
	}

	// "default" clears the override.
	if err := svc.SetUserCommentTone(admin, alice.ID, "default"); err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.CommentConfig().ToneByName["Alice"]; ok {
		t.Error(`"default" should clear Alice's override`)
	}

	// Deletes remove the entries.
	if err := svc.DeleteRivalry(admin, 0); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteCommentNote(admin, 0); err != nil {
		t.Fatal(err)
	}
	if cfg := svc.CommentConfig(); len(cfg.Rivalries) != 0 || len(cfg.Notes) != 0 {
		t.Errorf("expected empty after delete: %+v / %+v", cfg.Rivalries, cfg.Notes)
	}
}

func TestCommentConfigAdminGated(t *testing.T) {
	svc, _, _ := newTestService(t)
	player, _ := svc.Register("SHA256:p", testInvite, "Player")
	if err := svc.SetUserCommentTone(player, player.ID, "savage"); err == nil {
		t.Error("non-admin SetUserCommentTone should be rejected")
	}
	if err := svc.AddCommentNote(player, "x"); err == nil {
		t.Error("non-admin AddCommentNote should be rejected")
	}
	if err := svc.AddRivalry(player, 1, 2, "x"); err == nil {
		t.Error("non-admin AddRivalry should be rejected")
	}
}

type fakeCommentSource struct{ m map[int64]ai.Comment }

func (f fakeCommentSource) All(now time.Time) map[int64]ai.Comment { return f.m }

// TestLeaderboardCommentsScoping asserts the visibility boundary: on the main
// leaderboard EVERYONE — players and admins — sees only their own comment; the
// full set is admin-gated behind AllLeaderboardComments. No source ⇒ none.
func TestLeaderboardCommentsScoping(t *testing.T) {
	svc, _, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, testInvite, "Admin")
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")

	if got := svc.LeaderboardComments(alice); len(got) != 0 {
		t.Fatalf("no source should yield no comments, got %v", got)
	}

	svc.SetCommentSource(fakeCommentSource{m: map[int64]ai.Comment{
		admin.ID: {UserID: admin.ID, Text: "admin line"},
		alice.ID: {UserID: alice.ID, Text: "alice line"},
		bob.ID:   {UserID: bob.ID, Text: "bob line"},
	}})

	if got := svc.LeaderboardComments(alice); len(got) != 1 || got[alice.ID] != "alice line" {
		t.Fatalf("player should see only own comment, got %v", got)
	}
	// The admin's main-leaderboard view is now own-only too.
	if got := svc.LeaderboardComments(admin); len(got) != 1 || got[admin.ID] != "admin line" {
		t.Fatalf("admin should see only own comment on the leaderboard, got %v", got)
	}
	// The full set lives behind the admin-gated AllLeaderboardComments.
	if got, err := svc.AllLeaderboardComments(admin); err != nil || len(got) != 3 {
		t.Fatalf("admin AllLeaderboardComments should return all 3, got %v err=%v", got, err)
	}
	if _, err := svc.AllLeaderboardComments(alice); !errors.Is(err, ErrForbidden) {
		t.Fatalf("player AllLeaderboardComments should be forbidden, got %v", err)
	}

	// Muting a player hides their ALREADY-CACHED comment immediately (read-time
	// enforcement) from both their own view and the admin's full set.
	if err := svc.SetUserCommentTone(admin, bob.ID, "mute"); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.AllLeaderboardComments(admin); len(got) != 2 || got[bob.ID] != "" {
		t.Fatalf("muted Bob must vanish from the admin full set immediately, got %v", got)
	}
	if got := svc.LeaderboardComments(bob); len(got) != 0 {
		t.Fatalf("muted Bob must not see his own cached comment, got %v", got)
	}
	// A non-muted player is unaffected.
	if got := svc.LeaderboardComments(alice); got[alice.ID] != "alice line" {
		t.Fatalf("Alice should still see her comment, got %v", got)
	}
}
