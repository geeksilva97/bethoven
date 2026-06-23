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
