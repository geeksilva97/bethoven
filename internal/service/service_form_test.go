package service

import (
	"testing"
	"time"

	"bethoven/internal/models"
)

// addTeamMatch inserts a match with explicit team names/phase and returns its id.
func addTeamMatch(t *testing.T, store interface {
	CreateMatch(models.Match) (int64, error)
}, tournamentID int64, teamA, teamB string, phase models.Phase, startsAt time.Time) int64 {
	t.Helper()
	id, err := store.CreateMatch(models.Match{
		TournamentID: tournamentID, TeamA: teamA, TeamB: teamB,
		Phase: phase, StartsAt: startsAt,
	})
	if err != nil {
		t.Fatalf("CreateMatch: %v", err)
	}
	return id
}

func TestTeamForm(t *testing.T) {
	svc, store, fc := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")

	svc.SetTeamForms(map[string]string{
		"Brazil": "WWDLW", // baseline, oldest→newest
		"Wales":  "wdl",   // case-insensitive, shorter than 5
	})

	// Baseline only: no finished matches involving these teams yet.
	if got := svc.TeamForm("Brazil"); !equalForm(got, []models.FormOutcome{
		models.FormWin, models.FormWin, models.FormDraw, models.FormLoss, models.FormWin,
	}) {
		t.Fatalf("baseline Brazil = %v, want WWDLW", got)
	}
	if got := svc.TeamForm("Wales"); !equalForm(got, []models.FormOutcome{
		models.FormWin, models.FormDraw, models.FormLoss,
	}) {
		t.Errorf("baseline Wales = %v, want WDL", got)
	}
	// Unknown team, no baseline, no results.
	if got := svc.TeamForm("Atlantis"); got != nil {
		t.Errorf("unknown team = %v, want nil", got)
	}

	// Tournament matches involving Brazil, in kickoff order (oldest→newest):
	m1 := addTeamMatch(t, store, svc.tournamentID, "Brazil", "Chile", models.PhaseGroup, base.Add(1*time.Hour)) // A wins
	m2 := addTeamMatch(t, store, svc.tournamentID, "Peru", "Brazil", models.PhaseGroup, base.Add(2*time.Hour))  // B wins
	m3 := addTeamMatch(t, store, svc.tournamentID, "Brazil", "Egypt", models.PhaseGroup, base.Add(3*time.Hour)) // A loses
	m4 := addTeamMatch(t, store, svc.tournamentID, "Brazil", "Ghana", models.PhaseRound16, base.Add(4*time.Hour))

	fc.T = base.Add(1000 * time.Hour) // well past every kickoff
	mustResult(t, svc, admin, m1, 2, 0)
	mustResult(t, svc, admin, m2, 0, 3) // Peru 0 - 3 Brazil → Brazil (TeamB) wins
	mustResult(t, svc, admin, m3, 1, 2) // Brazil (TeamA) loses
	mustResult(t, svc, admin, m4, 1, 1) // regulation 1-1 a.e.t. → draw

	// baseline WWDLW ++ [W(m1) W(m2) L(m3) D(m4)] = 9 outcomes, trimmed to last 5.
	if got := svc.TeamForm("Brazil"); !equalForm(got, []models.FormOutcome{
		models.FormWin, models.FormWin, models.FormWin, models.FormLoss, models.FormDraw,
	}) {
		t.Errorf("merged Brazil = %v, want [W W W L D] (last 5)", got)
	}

	// Perspective check from the other side: Peru lost m2 (its only result, no baseline).
	if got := svc.TeamForm("Peru"); !equalForm(got, []models.FormOutcome{models.FormLoss}) {
		t.Errorf("Peru = %v, want [L]", got)
	}
}

func TestTeamResults(t *testing.T) {
	svc, store, fc := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")

	// No finished games yet.
	if got := svc.TeamResults("Brazil"); got != nil {
		t.Errorf("no games = %v, want nil", got)
	}

	// Matches in kickoff order (oldest→newest).
	m1 := addTeamMatch(t, store, svc.tournamentID, "Brazil", "Chile", models.PhaseGroup, base.Add(1*time.Hour)) // Brazil (A) wins
	m2 := addTeamMatch(t, store, svc.tournamentID, "Peru", "Brazil", models.PhaseGroup, base.Add(2*time.Hour))  // Brazil (B) wins
	m3 := addTeamMatch(t, store, svc.tournamentID, "Brazil", "Egypt", models.PhaseGroup, base.Add(3*time.Hour)) // Brazil (A) loses
	unplayed := addTeamMatch(t, store, svc.tournamentID, "Brazil", "Ghana", models.PhaseRound16, base.Add(4*time.Hour))
	_ = unplayed

	fc.T = base.Add(1000 * time.Hour)
	mustResult(t, svc, admin, m1, 2, 0)
	mustResult(t, svc, admin, m2, 0, 3) // Peru 0 - 3 Brazil
	mustResult(t, svc, admin, m3, 1, 2) // Brazil loses

	// Brazil's perspective: oldest→newest, the unplayed match excluded.
	want := []TeamGame{
		{Opponent: "Chile", GoalsFor: 2, GoalsAgainst: 0, Outcome: models.FormWin},
		{Opponent: "Peru", GoalsFor: 3, GoalsAgainst: 0, Outcome: models.FormWin},
		{Opponent: "Egypt", GoalsFor: 1, GoalsAgainst: 2, Outcome: models.FormLoss},
	}
	got := svc.TeamResults("Brazil")
	if len(got) != len(want) {
		t.Fatalf("TeamResults(Brazil) = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("game %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Opponent's perspective is mirrored: Peru lost its only game, 0-3.
	if peru := svc.TeamResults("Peru"); len(peru) != 1 ||
		peru[0] != (TeamGame{Opponent: "Brazil", GoalsFor: 0, GoalsAgainst: 3, Outcome: models.FormLoss}) {
		t.Errorf("TeamResults(Peru) = %+v, want one 0-3 loss to Brazil", peru)
	}

	// Unknown team yields nil.
	if got := svc.TeamResults("Atlantis"); got != nil {
		t.Errorf("unknown team = %v, want nil", got)
	}
}

// equalForm compares two form slices element-wise.
func equalForm(a, b []models.FormOutcome) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mustResult(t *testing.T, svc *Service, admin *models.User, matchID int64, a, b int) {
	t.Helper()
	if err := svc.EnterResult(admin, matchID, a, b); err != nil {
		t.Fatalf("EnterResult(%d, %d-%d): %v", matchID, a, b, err)
	}
}
