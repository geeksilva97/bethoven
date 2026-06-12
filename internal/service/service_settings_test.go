package service

import (
	"errors"
	"testing"
	"time"
)

// TestPublicBetsDefaultsOff: the setting is off until an admin enables it, and a
// player cannot see the all-bets grid in that state.
func TestPublicBetsDefaultsOff(t *testing.T) {
	svc, _, _ := newTestService(t)
	player, _ := svc.Register("SHA256:p", testInvite, "Player")

	enabled, err := svc.PublicBetsEnabled()
	if err != nil {
		t.Fatalf("PublicBetsEnabled: %v", err)
	}
	if enabled {
		t.Error("public bets should default to off")
	}
	if _, err := svc.PublicBetsGrid(player); !errors.Is(err, ErrForbidden) {
		t.Errorf("player should be forbidden while off, got %v", err)
	}
}

// TestSetPublicBetsRequiresAdmin: only an admin may flip the toggle.
func TestSetPublicBetsRequiresAdmin(t *testing.T) {
	svc, _, _ := newTestService(t)
	player, _ := svc.Register("SHA256:p", testInvite, "Player")
	admin, _ := svc.Register(adminFP, "", "Boss")

	if err := svc.SetPublicBets(player, true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin SetPublicBets should be forbidden, got %v", err)
	}
	if err := svc.SetPublicBets(admin, true); err != nil {
		t.Fatalf("admin SetPublicBets: %v", err)
	}
	enabled, _ := svc.PublicBetsEnabled()
	if !enabled {
		t.Error("public bets should be enabled after admin toggle")
	}
}

// TestPublicBetsGridRevealsOnlyAfterKickoff: once enabled, a player sees the
// grid, but it omits matches that have not yet kicked off — preserving blind
// betting — while including kicked-off/finished ones.
func TestPublicBetsGridRevealsOnlyAfterKickoff(t *testing.T) {
	svc, store, fc := newTestService(t)
	player, _ := svc.Register("SHA256:p", testInvite, "Player")
	other, _ := svc.Register("SHA256:o", testInvite, "Other")
	admin, _ := svc.Register(adminFP, "", "Boss")

	// One match already kicked off, one still in the future.
	started := addMatch(t, store, svc.tournamentID, base.Add(-time.Hour))
	upcoming := addMatch(t, store, svc.tournamentID, base.Add(time.Hour))

	// Both players had bet on each before their kickoffs (clock rewound for the
	// upcoming one is unnecessary — it's still in the future at `base`).
	fc.T = base.Add(-2 * time.Hour)
	if err := svc.PlaceBet(other.ID, started, 2, 1); err != nil {
		t.Fatalf("bet started: %v", err)
	}
	fc.T = base
	if err := svc.PlaceBet(other.ID, upcoming, 0, 0); err != nil {
		t.Fatalf("bet upcoming: %v", err)
	}

	if err := svc.SetPublicBets(admin, true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	grid, err := svc.PublicBetsGrid(player)
	if err != nil {
		t.Fatalf("PublicBetsGrid: %v", err)
	}
	if len(grid.Matches) != 1 || grid.Matches[0].ID != started {
		t.Fatalf("grid should contain only the kicked-off match, got %+v", grid.Matches)
	}
	if _, ok := grid.Cells[upcoming]; ok {
		t.Error("upcoming match picks must not be revealed before kickoff")
	}
	if _, ok := grid.Cells[started][other.ID]; !ok {
		t.Error("kicked-off match picks should be visible")
	}
}

// TestAllBetsStillShowsEverything: the admin grid is unchanged by the public-bets
// machinery — it includes upcoming matches.
func TestAllBetsStillShowsEverything(t *testing.T) {
	svc, store, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")
	addMatch(t, store, svc.tournamentID, base.Add(-time.Hour))
	addMatch(t, store, svc.tournamentID, base.Add(time.Hour))

	grid, err := svc.AllBets(admin)
	if err != nil {
		t.Fatalf("AllBets: %v", err)
	}
	if len(grid.Matches) != 2 {
		t.Errorf("admin grid should include all matches, got %d", len(grid.Matches))
	}

	// And an admin may always view the public grid even while disabled.
	if _, err := svc.PublicBetsGrid(admin); err != nil {
		t.Errorf("admin PublicBetsGrid while disabled: %v", err)
	}
}

// TestPublicBetsTotalsIgnoreHiddenMatches: hiding an unstarted match never
// changes totals (unstarted matches score 0), so a player's public-grid total
// matches the admin grid total for the same player.
func TestPublicBetsTotalsIgnoreHiddenMatches(t *testing.T) {
	svc, store, fc := newTestService(t)
	player, _ := svc.Register("SHA256:p", testInvite, "Player")
	admin, _ := svc.Register(adminFP, "", "Boss")

	finished := addMatch(t, store, svc.tournamentID, base.Add(-time.Hour))
	upcoming := addMatch(t, store, svc.tournamentID, base.Add(time.Hour))

	fc.T = base.Add(-2 * time.Hour)
	if err := svc.PlaceBet(player.ID, finished, 2, 1); err != nil {
		t.Fatalf("bet finished: %v", err)
	}
	fc.T = base
	if err := svc.PlaceBet(player.ID, upcoming, 1, 0); err != nil {
		t.Fatalf("bet upcoming: %v", err)
	}
	// Exact-score result on the finished match: 3 points.
	if err := svc.EnterResult(admin, finished, 2, 1); err != nil {
		t.Fatalf("EnterResult: %v", err)
	}

	if err := svc.SetPublicBets(admin, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	pub, err := svc.PublicBetsGrid(player)
	if err != nil {
		t.Fatalf("PublicBetsGrid: %v", err)
	}
	full, _ := svc.AllBets(admin)
	if pub.Totals[player.ID] != full.Totals[player.ID] {
		t.Errorf("public total %d != admin total %d", pub.Totals[player.ID], full.Totals[player.ID])
	}
	if pub.Totals[player.ID] != 3 {
		t.Errorf("expected 3 points, got %d", pub.Totals[player.ID])
	}
}
