package service

import (
	"errors"
	"testing"
	"time"

	"bethoven/internal/models"
)

// EditMatch corrects a fixture's teams/phase/group/kickoff and leaves the
// recorded result alone.
func TestEditMatch(t *testing.T) {
	svc, store, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")

	id, err := svc.AddMatch(admin, "Netherlands", "Marocco", models.PhaseRound32, "", base.Add(time.Hour))
	if err != nil {
		t.Fatalf("AddMatch: %v", err)
	}
	kick := base.Add(48 * time.Hour)
	if err := svc.EditMatch(admin, id, "Netherlands", "Morocco", models.PhaseRound16, "Group X", kick); err != nil {
		t.Fatalf("EditMatch: %v", err)
	}
	got, err := store.MatchByID(id)
	if err != nil {
		t.Fatalf("MatchByID: %v", err)
	}
	if got.TeamB != "Morocco" || got.Phase != models.PhaseRound16 || got.GroupLabel != "Group X" {
		t.Errorf("edit not applied: %+v", got)
	}
	if !got.StartsAt.Equal(kick.UTC()) {
		t.Errorf("kickoff not updated: got %v want %v", got.StartsAt, kick.UTC())
	}
}

// EditMatch trims and rejects team names with control/escape chars (they render
// into every player's terminal).
func TestEditMatchValidatesTeams(t *testing.T) {
	svc, _, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")
	id, _ := svc.AddMatch(admin, "A", "B", models.PhaseGroup, "", base.Add(time.Hour))

	if err := svc.EditMatch(admin, id, "A", "B\x1b[2J", models.PhaseGroup, "", base.Add(time.Hour)); !errors.Is(err, ErrTeamInvalid) {
		t.Errorf("expected ErrTeamInvalid for escape in team, got %v", err)
	}
	if err := svc.EditMatch(admin, id, "  ", "B", models.PhaseGroup, "", base.Add(time.Hour)); !errors.Is(err, ErrTeamRequired) {
		t.Errorf("expected ErrTeamRequired for blank team, got %v", err)
	}
}

// Editing and deleting are admin-only.
func TestEditDeleteRequireAdmin(t *testing.T) {
	svc, _, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")
	player, _ := svc.Register("SHA256:p", testInvite, "Pat")
	id, _ := svc.AddMatch(admin, "A", "B", models.PhaseGroup, "", base.Add(time.Hour))

	if err := svc.EditMatch(player, id, "A", "C", models.PhaseGroup, "", base.Add(time.Hour)); !errors.Is(err, ErrForbidden) {
		t.Errorf("EditMatch by player should be forbidden, got %v", err)
	}
	if _, err := svc.DeleteMatch(player, id); !errors.Is(err, ErrForbidden) {
		t.Errorf("DeleteMatch by player should be forbidden, got %v", err)
	}
	if _, err := svc.CountMatchBets(player, id); !errors.Is(err, ErrForbidden) {
		t.Errorf("CountMatchBets by player should be forbidden, got %v", err)
	}
}

// DeleteMatch removes the match and any bets on it, returning the bet count.
func TestDeleteMatchCascadesBets(t *testing.T) {
	svc, store, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")
	player, _ := svc.Register("SHA256:p", testInvite, "Pat")

	id, _ := svc.AddMatch(admin, "A", "B", models.PhaseGroup, "", base.Add(time.Hour))
	if err := svc.PlaceBet(player.ID, id, 1, 0); err != nil {
		t.Fatalf("PlaceBet: %v", err)
	}
	n, err := svc.CountMatchBets(admin, id)
	if err != nil || n != 1 {
		t.Fatalf("CountMatchBets = %d, %v; want 1", n, err)
	}

	deleted, err := svc.DeleteMatch(admin, id)
	if err != nil {
		t.Fatalf("DeleteMatch: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted bets = %d, want 1", deleted)
	}
	if _, err := store.MatchByID(id); err == nil {
		t.Errorf("match should be gone after delete")
	}
	if bets, _ := store.BetsForMatch(id); len(bets) != 0 {
		t.Errorf("bets should be gone after delete, got %d", len(bets))
	}
}

// Editing or deleting a missing match reports not-found, not a silent success.
func TestEditDeleteMissingMatch(t *testing.T) {
	svc, _, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")
	if err := svc.EditMatch(admin, 999, "A", "B", models.PhaseGroup, "", base); !errors.Is(err, ErrMatchNotFound) {
		t.Errorf("EditMatch missing = %v, want ErrMatchNotFound", err)
	}
	if _, err := svc.DeleteMatch(admin, 999); !errors.Is(err, ErrMatchNotFound) {
		t.Errorf("DeleteMatch missing = %v, want ErrMatchNotFound", err)
	}
}
