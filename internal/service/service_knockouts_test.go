package service

import (
	"testing"
	"time"

	"bethoven/internal/models"
)

func TestKnockoutPicture(t *testing.T) {
	svc, store, _ := newTestService(t)

	admin, err := store.CreateUser(adminFP, "Admin", models.RoleAdmin, base)
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	kickoff := base.Add(time.Hour)
	addGroup := func(a, b string) int64 {
		id, err := svc.AddMatch(admin, a, b, models.PhaseGroup, "Group A", kickoff)
		if err != nil {
			t.Fatalf("add %s v %s: %v", a, b, err)
		}
		return id
	}
	result := func(id int64, sa, sb int) {
		if err := svc.EnterResult(admin, id, sa, sb); err != nil {
			t.Fatalf("enter result: %v", err)
		}
	}

	// Group A: Mexico 9, South Korea 4, Czechia 2, South Africa 1.
	result(addGroup("Mexico", "South Africa"), 2, 0)
	result(addGroup("South Korea", "Czechia"), 1, 1)
	result(addGroup("Mexico", "South Korea"), 1, 0)
	result(addGroup("Czechia", "South Africa"), 0, 0)
	result(addGroup("Mexico", "Czechia"), 3, 0)
	result(addGroup("South Africa", "South Korea"), 0, 2)

	// A knockout match the admin has entered (no result yet).
	if _, err := svc.AddMatch(admin, "Mexico", "Brazil", models.PhaseRound32, "", base.Add(48*time.Hour)); err != nil {
		t.Fatalf("add knockout: %v", err)
	}

	pic, err := svc.KnockoutPicture()
	if err != nil {
		t.Fatalf("KnockoutPicture: %v", err)
	}

	// Group table computed and ordered.
	if len(pic.Groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(pic.Groups))
	}
	g := pic.Groups[0]
	wantOrder := []string{"Mexico", "South Korea", "Czechia", "South Africa"}
	for i, team := range wantOrder {
		if g.Rows[i].Team != team {
			t.Errorf("group rank %d: want %s, got %s", i+1, team, g.Rows[i].Team)
		}
	}
	if g.Rows[0].Pts != 9 {
		t.Errorf("Mexico should have 9 pts, got %d", g.Rows[0].Pts)
	}

	// Third-place race: Czechia is the lone third, so it ranks 1 and qualifies.
	if len(pic.ThirdPlace) != 1 {
		t.Fatalf("want 1 third-placed team, got %d", len(pic.ThirdPlace))
	}
	if tp := pic.ThirdPlace[0]; tp.Team != "Czechia" || !tp.Qualifies {
		t.Errorf("third place wrong: %+v", tp)
	}

	// Bracket: the full ladder is present; the R32 match landed in its round.
	if len(pic.Bracket) != len(knockoutPhases) {
		t.Fatalf("want %d bracket rounds, got %d", len(knockoutPhases), len(pic.Bracket))
	}
	r32 := pic.Bracket[0]
	if r32.Phase != models.PhaseRound32 || r32.Label != "Round of 32" {
		t.Errorf("first round should be Round of 32, got %q (%s)", r32.Label, r32.Phase)
	}
	if len(r32.Matches) != 1 || r32.Matches[0].TeamB != "Brazil" {
		t.Fatalf("R32 should hold the one entered match, got %+v", r32.Matches)
	}
	// Later rounds are not drawn yet.
	if len(pic.Bracket[1].Matches) != 0 {
		t.Errorf("Round of 16 should be empty, got %+v", pic.Bracket[1].Matches)
	}
}

func TestEliminatedTeams(t *testing.T) {
	score := func(n int) *int { return &n }
	fin := func(a, b string, sa, sb int) models.Match {
		return models.Match{TeamA: a, TeamB: b, Finished: true, ScoreA: score(sa), ScoreB: score(sb)}
	}
	// R32 settled (one decisive, one 1-1 draw decided on penalties), R16 drawn with
	// the penalty winner (PenWin) in it.
	bracket := []BracketRound{
		{Phase: models.PhaseRound32, Matches: []models.Match{
			fin("WinA", "OutB", 2, 0),  // decisive: OutB out
			fin("PenWin", "PenLose", 1, 1), // draw: only resolvable once R16 is drawn
		}},
		{Phase: models.PhaseRound16, Matches: []models.Match{
			{TeamA: "WinA", TeamB: "PenWin"}, // not played yet, just drawn
		}},
	}
	elim := eliminatedTeams(bracket)

	if !elim["OutB"] {
		t.Error("decisive 90' loser OutB should be eliminated")
	}
	if !elim["PenLose"] {
		t.Error("penalty loser PenLose should be eliminated once R16 is drawn without it")
	}
	if elim["WinA"] || elim["PenWin"] {
		t.Errorf("teams in the furthest drawn round must not be eliminated: %v", elim)
	}
}

func TestEliminatedTeamsNoInferenceFromDraw(t *testing.T) {
	score := func(n int) *int { return &n }
	// A 1-1 draw with NO later round drawn: the shootout winner is unknown, so
	// neither side may be flagged — advancement is never inferred from 90'.
	bracket := []BracketRound{
		{Phase: models.PhaseRound32, Matches: []models.Match{
			{TeamA: "X", TeamB: "Y", Finished: true, ScoreA: score(1), ScoreB: score(1)},
		}},
	}
	if elim := eliminatedTeams(bracket); elim["X"] || elim["Y"] {
		t.Errorf("a 90' draw with no next round must eliminate nobody, got %v", elim)
	}
}

func TestEnterPenaltiesResolvesDraw(t *testing.T) {
	svc, store, _ := newTestService(t)
	admin, err := store.CreateUser(adminFP, "Admin", models.RoleAdmin, base)
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	id, err := svc.AddMatch(admin, "Germany", "Paraguay", models.PhaseRound32, "", base.Add(time.Hour))
	if err != nil {
		t.Fatalf("add knockout: %v", err)
	}

	// Penalties before a result exists: not applicable.
	if err := svc.EnterPenalties(admin, id, 4, 2); err != ErrPenaltiesNotApplicable {
		t.Fatalf("pens before result: want ErrPenaltiesNotApplicable, got %v", err)
	}

	if err := svc.EnterResult(admin, id, 1, 1); err != nil {
		t.Fatalf("enter draw: %v", err)
	}

	// A 90' draw alone eliminates nobody — advancement is unknown.
	pic, _ := svc.KnockoutPicture()
	if pic.Eliminated["Germany"] || pic.Eliminated["Paraguay"] {
		t.Fatalf("draw with no pens must eliminate nobody, got %v", pic.Eliminated)
	}

	// Validation: tied pens and non-admin are rejected.
	if err := svc.EnterPenalties(admin, id, 3, 3); err != ErrPenaltiesTied {
		t.Fatalf("tied pens: want ErrPenaltiesTied, got %v", err)
	}
	if err := svc.EnterPenalties(nil, id, 4, 2); err != ErrForbidden {
		t.Fatalf("non-admin pens: want ErrForbidden, got %v", err)
	}

	// Germany wins the shootout 4-2 → Paraguay is eliminated, Germany is not.
	if err := svc.EnterPenalties(admin, id, 4, 2); err != nil {
		t.Fatalf("enter pens: %v", err)
	}
	pic, _ = svc.KnockoutPicture()
	if !pic.Eliminated["Paraguay"] {
		t.Error("shootout loser Paraguay should be eliminated")
	}
	if pic.Eliminated["Germany"] {
		t.Error("shootout winner Germany must not be eliminated")
	}

	// The shootout never changes the scored 90' result.
	m, _ := svc.Match(id)
	if *m.ScoreA != 1 || *m.ScoreB != 1 || *m.PenA != 4 || *m.PenB != 2 {
		t.Errorf("unexpected stored match: 90'=%d-%d pens=%v-%v", *m.ScoreA, *m.ScoreB, m.PenA, m.PenB)
	}

	// Re-entering a decisive 90' result clears the stale shootout.
	if err := svc.EnterResult(admin, id, 2, 1); err != nil {
		t.Fatalf("correct result: %v", err)
	}
	m, _ = svc.Match(id)
	if m.PenA != nil || m.PenB != nil {
		t.Errorf("re-entering a result should clear penalties, got %v-%v", m.PenA, m.PenB)
	}
}
