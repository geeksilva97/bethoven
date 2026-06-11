package service

import (
	"errors"
	"testing"
	"time"

	"bethoven/internal/models"
)

// addMatch inserts a match starting at the given time and returns its id.
func addMatch(t *testing.T, store interface {
	CreateMatch(models.Match) (int64, error)
}, tournamentID int64, startsAt time.Time) int64 {
	t.Helper()
	id, err := store.CreateMatch(models.Match{
		TournamentID: tournamentID, TeamA: "A", TeamB: "B",
		Phase: models.PhaseGroup, StartsAt: startsAt,
	})
	if err != nil {
		t.Fatalf("CreateMatch: %v", err)
	}
	return id
}

// TestKickoffLock is the headline test: a bet is accepted and editable before
// kickoff, and rejected the moment the clock reaches the start time.
func TestKickoffLock(t *testing.T) {
	svc, store, fc := newTestService(t)
	user, _ := svc.Register("SHA256:p", testInvite, "Player")
	kickoff := base.Add(2 * time.Hour)
	mid := addMatch(t, store, svc.tournamentID, kickoff)

	// Before kickoff: accepted.
	if err := svc.PlaceBet(user.ID, mid, 2, 1); err != nil {
		t.Fatalf("pre-kickoff bet should succeed: %v", err)
	}
	// Still before kickoff: editable.
	if err := svc.PlaceBet(user.ID, mid, 3, 0); err != nil {
		t.Fatalf("pre-kickoff edit should succeed: %v", err)
	}
	got, _ := svc.MyBet(user.ID, mid)
	if got == nil || got.PredA != 3 || got.PredB != 0 {
		t.Errorf("edit not persisted: %+v", got)
	}

	// Advance to exactly kickoff: locked.
	fc.T = kickoff
	if err := svc.PlaceBet(user.ID, mid, 1, 1); !errors.Is(err, ErrMatchLocked) {
		t.Errorf("bet at kickoff should be locked, got %v", err)
	}

	// Past kickoff: still locked, and the stored bet is unchanged.
	fc.Add(time.Hour)
	if err := svc.PlaceBet(user.ID, mid, 0, 5); !errors.Is(err, ErrMatchLocked) {
		t.Errorf("bet after kickoff should be locked, got %v", err)
	}
	got, _ = svc.MyBet(user.ID, mid)
	if got.PredA != 3 || got.PredB != 0 {
		t.Errorf("locked bet was mutated: %+v", got)
	}
}

func TestPlaceBetValidatesScores(t *testing.T) {
	svc, store, _ := newTestService(t)
	user, _ := svc.Register("SHA256:p", testInvite, "Player")
	mid := addMatch(t, store, svc.tournamentID, base.Add(time.Hour))

	if err := svc.PlaceBet(user.ID, mid, -1, 0); !errors.Is(err, ErrInvalidScore) {
		t.Errorf("negative score should be rejected, got %v", err)
	}
	if err := svc.PlaceBet(user.ID, mid, 0, 100); !errors.Is(err, ErrInvalidScore) {
		t.Errorf("out-of-range score should be rejected, got %v", err)
	}
}

// TestCannotBetFinishedMatch enforces the invariant that an ended match is
// closed even if its listed start time is somehow still in the future.
func TestCannotBetFinishedMatch(t *testing.T) {
	svc, store, _ := newTestService(t)
	user, _ := svc.Register("SHA256:p", testInvite, "Player")
	admin, _ := svc.Register(adminFP, "", "Boss")

	// Match nominally starts in the future, but the admin records a result.
	mid := addMatch(t, store, svc.tournamentID, base.Add(time.Hour))
	if err := svc.EnterResult(admin, mid, 1, 0); err != nil {
		t.Fatalf("EnterResult: %v", err)
	}

	if err := svc.PlaceBet(user.ID, mid, 2, 2); !errors.Is(err, ErrMatchLocked) {
		t.Errorf("betting a finished match must be locked, got %v", err)
	}
}

func TestPlaceBetUnknownMatch(t *testing.T) {
	svc, _, _ := newTestService(t)
	user, _ := svc.Register("SHA256:p", testInvite, "Player")
	if err := svc.PlaceBet(user.ID, 9999, 1, 0); !errors.Is(err, ErrMatchNotFound) {
		t.Errorf("expected ErrMatchNotFound, got %v", err)
	}
}
