package service

import (
	"errors"
	"testing"
	"time"

	"bethoven/internal/ai"
	"bethoven/internal/models"
)

// TestAIBetsSourcedFromDB checks that BETanIA's picks-on-record come from the DB
// (so they survive a restart, unlike the in-memory activity ring): the points of
// finished matches are scored, upcoming matches carry no points, ordering is
// most-recently-decided first, and the admin gate + not-onboarded paths hold.
func TestAIBetsSourcedFromDB(t *testing.T) {
	svc, store, fc := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")

	// Not onboarded yet -> ErrAIOff.
	if _, err := svc.AIBets(admin, 0); !errors.Is(err, ErrAIOff) {
		t.Fatalf("expected ErrAIOff before onboarding, got %v", err)
	}

	// Onboard BETanIA (system account; no real SSH key).
	bot, err := store.CreateUser(ai.Fingerprint, "BETanIA 🤖", models.RolePlayer, fc.Now())
	if err != nil {
		t.Fatalf("create BETanIA: %v", err)
	}

	past := addMatch(t, store, svc.tournamentID, base.Add(-2*time.Hour))
	future := addMatch(t, store, svc.tournamentID, base.Add(48*time.Hour))

	// Bet the already-played match first, the upcoming one a minute later, so the
	// upcoming pick has the newer UpdatedAt and must sort first.
	if err := store.UpsertBet(models.Bet{UserID: bot.ID, MatchID: past, PredA: 2, PredB: 0}, fc.Now()); err != nil {
		t.Fatalf("seed past bet: %v", err)
	}
	if err := store.UpsertBet(models.Bet{UserID: bot.ID, MatchID: future, PredA: 1, PredB: 1}, fc.Now().Add(time.Minute)); err != nil {
		t.Fatalf("seed future bet: %v", err)
	}
	// Settle the past match exactly (3 pts under Classic).
	if err := svc.EnterResult(admin, past, 2, 0); err != nil {
		t.Fatalf("EnterResult: %v", err)
	}

	bets, err := svc.AIBets(admin, 0)
	if err != nil {
		t.Fatalf("AIBets: %v", err)
	}
	if len(bets) != 2 {
		t.Fatalf("expected 2 picks on record, got %d", len(bets))
	}
	// Newest-decided first: the upcoming match leads, with no points.
	if bets[0].Match.ID != future || bets[0].Points != 0 || bets[0].Match.Finished {
		t.Errorf("expected upcoming match first with 0 pts, got %+v", bets[0])
	}
	if bets[1].Match.ID != past || !bets[1].Match.Finished || bets[1].Points != 3 {
		t.Errorf("expected finished past match with 3 pts second, got %+v", bets[1])
	}

	// Players are forbidden from reading BETanIA's picks.
	player, _ := svc.Register("SHA256:nosy", testInvite, "Nosy")
	if _, err := svc.AIBets(player, 0); !errors.Is(err, ErrForbidden) {
		t.Errorf("player AIBets should be forbidden, got %v", err)
	}
}
