package service

import (
	"testing"
	"time"

	"bethoven/internal/models"
	"bethoven/internal/results"
)

// addTeamMatch inserts a group fixture with explicit team names and kickoff.
func addTeamMatch(t *testing.T, store interface {
	CreateMatch(models.Match) (int64, error)
}, tournamentID int64, teamA, teamB string, startsAt time.Time) int64 {
	t.Helper()
	id, err := store.CreateMatch(models.Match{
		TournamentID: tournamentID, TeamA: teamA, TeamB: teamB,
		Phase: models.PhaseGroup, StartsAt: startsAt,
	})
	if err != nil {
		t.Fatalf("CreateMatch: %v", err)
	}
	return id
}

func score(a, b int) *results.Score { return &results.Score{A: a, B: b} }

// TestTeamNamesReconcile pins the verified team-name mismatches between the
// ESPN "fifa.world" scoreboard and fixtures.json: each pair must normalise to
// the same canonical string, or reconciliation silently misses that match.
// Pairs where ESPN already agrees with us are included as a regression guard.
func TestTeamNamesReconcile(t *testing.T) {
	pairs := []struct{ feed, ours string }{
		{"Bosnia-Herzegovina", "Bosnia & Herzegovina"},
		{"Congo DR", "DR Congo"},
		{"Czechia", "Czech Republic"},
		{"United States", "USA"},
		{"Türkiye", "Turkey"},
		{"Cape Verde", "Cape Verde"},   // ESPN agrees — guard against drift
		{"South Korea", "South Korea"}, // ESPN agrees — guard against drift
		{"Ivory Coast", "Ivory Coast"}, // ESPN agrees — guard against drift
	}
	for _, p := range pairs {
		if got, want := normTeam(p.feed), normTeam(p.ours); got != want {
			t.Errorf("feed %q -> %q but fixture %q -> %q (won't reconcile)", p.feed, got, p.ours, want)
		}
	}
}

// TestApplyFeedResultsRecordsAndIsIdempotent: a finished group match is scored,
// its feed link is persisted, and a second pass is a no-op.
func TestApplyFeedResultsRecordsAndIsIdempotent(t *testing.T) {
	svc, store, _ := newTestService(t)
	kick := base.Add(time.Hour)
	mid := addTeamMatch(t, store, svc.tournamentID, "Brazil", "Morocco", kick)

	feed := []results.FeedMatch{{
		ExternalRef: "100", HomeTeam: "Brazil", AwayTeam: "Morocco",
		KickoffUTC: kick, Finished: true, Reg90: score(2, 1),
	}}

	rep, err := svc.ApplyFeedResults(feed)
	if err != nil {
		t.Fatalf("ApplyFeedResults: %v", err)
	}
	if rep.Applied != 1 || rep.Skipped != 0 || len(rep.Unmatched) != 0 {
		t.Fatalf("first pass report = %+v, want applied=1", rep)
	}
	m, _ := store.MatchByID(mid)
	if !m.Finished || m.ScoreA == nil || *m.ScoreA != 2 || *m.ScoreB != 1 {
		t.Errorf("match not recorded correctly: %+v", m)
	}
	if m.ExternalRef != "100" {
		t.Errorf("feed link not persisted, external_ref=%q", m.ExternalRef)
	}

	// Second pass: already finished -> skipped, never overwritten.
	rep, err = svc.ApplyFeedResults(feed)
	if err != nil {
		t.Fatalf("second ApplyFeedResults: %v", err)
	}
	if rep.Applied != 0 || rep.Skipped != 1 {
		t.Errorf("idempotent pass report = %+v, want applied=0 skipped=1", rep)
	}
}

// TestApplyFeedResultsOrientation: when our fixture stores teams in the opposite
// order from the feed's home/away, the score is re-oriented to team_a/team_b.
func TestApplyFeedResultsOrientation(t *testing.T) {
	svc, store, _ := newTestService(t)
	kick := base.Add(time.Hour)
	// Our team_a = Morocco, team_b = Brazil — reversed vs the feed below.
	mid := addTeamMatch(t, store, svc.tournamentID, "Morocco", "Brazil", kick)

	feed := []results.FeedMatch{{
		ExternalRef: "101", HomeTeam: "Brazil", AwayTeam: "Morocco",
		KickoffUTC: kick, Finished: true, Reg90: score(2, 1), // Brazil 2, Morocco 1
	}}
	if _, err := svc.ApplyFeedResults(feed); err != nil {
		t.Fatalf("ApplyFeedResults: %v", err)
	}
	m, _ := store.MatchByID(mid)
	// team_a is Morocco -> 1; team_b is Brazil -> 2.
	if *m.ScoreA != 1 || *m.ScoreB != 2 {
		t.Errorf("orientation wrong: score_a=%d score_b=%d, want 1/2", *m.ScoreA, *m.ScoreB)
	}
}

// TestApplyFeedResultsTeamAlias: a feed team name that differs from ours still
// reconciles through teamAliases.
func TestApplyFeedResultsTeamAlias(t *testing.T) {
	svc, store, _ := newTestService(t)
	kick := base.Add(time.Hour)
	mid := addTeamMatch(t, store, svc.tournamentID, "Czech Republic", "Mexico", kick)

	feed := []results.FeedMatch{{
		ExternalRef: "102", HomeTeam: "Czechia", AwayTeam: "Mexico",
		KickoffUTC: kick, Finished: true, Reg90: score(0, 3),
	}}
	rep, err := svc.ApplyFeedResults(feed)
	if err != nil {
		t.Fatalf("ApplyFeedResults: %v", err)
	}
	if rep.Applied != 1 {
		t.Fatalf("alias match not applied: %+v", rep)
	}
	m, _ := store.MatchByID(mid)
	if *m.ScoreA != 0 || *m.ScoreB != 3 {
		t.Errorf("alias-matched score wrong: %d/%d", *m.ScoreA, *m.ScoreB)
	}
}

// TestApplyFeedResultsNeverOverwritesManualEntry: a result the admin already
// entered is preserved; the feed still links the fixture but does not overwrite.
func TestApplyFeedResultsNeverOverwritesManualEntry(t *testing.T) {
	svc, store, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")
	kick := base.Add(time.Hour)
	mid := addTeamMatch(t, store, svc.tournamentID, "Brazil", "Morocco", kick)

	if err := svc.EnterResult(admin, mid, 3, 0); err != nil {
		t.Fatalf("EnterResult: %v", err)
	}

	feed := []results.FeedMatch{{
		ExternalRef: "103", HomeTeam: "Brazil", AwayTeam: "Morocco",
		KickoffUTC: kick, Finished: true, Reg90: score(1, 1),
	}}
	rep, err := svc.ApplyFeedResults(feed)
	if err != nil {
		t.Fatalf("ApplyFeedResults: %v", err)
	}
	if rep.Applied != 0 || rep.Skipped != 1 {
		t.Errorf("report = %+v, want applied=0 skipped=1 (manual entry wins)", rep)
	}
	m, _ := store.MatchByID(mid)
	if *m.ScoreA != 3 || *m.ScoreB != 0 {
		t.Errorf("manual entry was overwritten: %d/%d, want 3/0", *m.ScoreA, *m.ScoreB)
	}
	if m.ExternalRef != "103" {
		t.Errorf("feed link should still be persisted, got %q", m.ExternalRef)
	}
}

// TestApplyFeedResultsSkipsNonFinalAndUnknown: in-play matches are ignored, and
// a finished feed match with no local fixture is reported unmatched.
func TestApplyFeedResultsSkipsNonFinalAndUnknown(t *testing.T) {
	svc, store, _ := newTestService(t)
	kick := base.Add(time.Hour)
	mid := addTeamMatch(t, store, svc.tournamentID, "Brazil", "Morocco", kick)

	feed := []results.FeedMatch{
		{ExternalRef: "200", HomeTeam: "Brazil", AwayTeam: "Morocco", KickoffUTC: kick, Finished: false},                 // in play
		{ExternalRef: "201", HomeTeam: "Spain", AwayTeam: "Japan", KickoffUTC: kick, Finished: true, Reg90: score(1, 0)}, // unknown
	}
	rep, err := svc.ApplyFeedResults(feed)
	if err != nil {
		t.Fatalf("ApplyFeedResults: %v", err)
	}
	if rep.Applied != 0 {
		t.Errorf("nothing should be applied, got %+v", rep)
	}
	if len(rep.Unmatched) != 1 {
		t.Errorf("expected 1 unmatched, got %v", rep.Unmatched)
	}
	// The in-play match must not have been linked or recorded.
	m, _ := store.MatchByID(mid)
	if m.Finished || m.ExternalRef != "" {
		t.Errorf("in-play feed match should not touch fixture: %+v", m)
	}
}

// TestApplyFeedResultsKnockoutNoReg90: a finished knockout whose 90' score can't
// be derived is left for the admin (skipped), though the fixture is linked.
func TestApplyFeedResultsKnockoutNoReg90(t *testing.T) {
	svc, store, _ := newTestService(t)
	kick := base.Add(time.Hour)
	mid := addTeamMatch(t, store, svc.tournamentID, "Brazil", "Morocco", kick)

	feed := []results.FeedMatch{{
		ExternalRef: "300", HomeTeam: "Brazil", AwayTeam: "Morocco",
		KickoffUTC: kick, Finished: true, Reg90: nil, // e.g. ET with no regulation-time breakdown
	}}
	rep, err := svc.ApplyFeedResults(feed)
	if err != nil {
		t.Fatalf("ApplyFeedResults: %v", err)
	}
	if rep.Applied != 0 || rep.Skipped != 1 {
		t.Errorf("report = %+v, want applied=0 skipped=1", rep)
	}
	m, _ := store.MatchByID(mid)
	if m.Finished {
		t.Errorf("match must stay open for the admin, got finished")
	}
}

// TestApplyFeedResultsKickoffWindow: same teams but a kickoff far outside the
// reconciliation window must not match by name.
func TestApplyFeedResultsKickoffWindow(t *testing.T) {
	svc, store, _ := newTestService(t)
	mid := addTeamMatch(t, store, svc.tournamentID, "Brazil", "Morocco", base)

	feed := []results.FeedMatch{{
		ExternalRef: "400", HomeTeam: "Brazil", AwayTeam: "Morocco",
		KickoffUTC: base.Add(10 * 24 * time.Hour), // 10 days away
		Finished:   true, Reg90: score(1, 0),
	}}
	rep, err := svc.ApplyFeedResults(feed)
	if err != nil {
		t.Fatalf("ApplyFeedResults: %v", err)
	}
	if rep.Applied != 0 || len(rep.Unmatched) != 1 {
		t.Errorf("far-off kickoff should not match: %+v", rep)
	}
	if m, _ := store.MatchByID(mid); m.ExternalRef != "" {
		t.Errorf("fixture should not have been linked, got %q", m.ExternalRef)
	}
}

// TestApplyFeedResultsUpdatesLeaderboard proves the headline win: once the feed
// records a result, the leaderboard reflects it with no other action.
func TestApplyFeedResultsUpdatesLeaderboard(t *testing.T) {
	svc, store, _ := newTestService(t)
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")
	kick := base.Add(time.Hour)
	mid := addTeamMatch(t, store, svc.tournamentID, "Brazil", "Morocco", kick)

	_ = svc.PlaceBet(alice.ID, mid, 2, 1) // exact -> 3
	_ = svc.PlaceBet(bob.ID, mid, 1, 0)   // result only -> 1

	feed := []results.FeedMatch{{
		ExternalRef: "500", HomeTeam: "Brazil", AwayTeam: "Morocco",
		KickoffUTC: kick, Finished: true, Reg90: score(2, 1),
	}}
	if _, err := svc.ApplyFeedResults(feed); err != nil {
		t.Fatalf("ApplyFeedResults: %v", err)
	}

	board, err := svc.Leaderboard()
	if err != nil {
		t.Fatal(err)
	}
	if board[0].User.ID != alice.ID || board[0].Total != 3 {
		t.Errorf("leader = %+v, want Alice/3", board[0])
	}
	if board[1].User.ID != bob.ID || board[1].Total != 1 {
		t.Errorf("second = %+v, want Bob/1", board[1])
	}
}
