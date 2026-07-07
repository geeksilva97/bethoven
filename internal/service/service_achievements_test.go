package service

import (
	"errors"
	"testing"
	"time"

	"bethoven/internal/achievements"
	"bethoven/internal/models"
)

// boardHolders returns the holder names for one badge on the computed board,
// read as the given admin.
func boardHolders(t *testing.T, svc *Service, by *models.User, badge achievements.Badge) []string {
	t.Helper()
	board, err := svc.Achievements(by)
	if err != nil {
		t.Fatalf("Achievements: %v", err)
	}
	for _, st := range board.Standings {
		if st.Badge.ID == badge.ID {
			names := make([]string, len(st.Holders))
			for i, h := range st.Holders {
				names[i] = h.Name
			}
			return names
		}
	}
	t.Fatalf("badge %q not on the board", badge.ID)
	return nil
}

// enterResult finishes a match as the admin.
func enterResult(t *testing.T, svc *Service, admin *models.User, mid int64, a, b int) {
	t.Helper()
	if err := svc.EnterResult(admin, mid, a, b); err != nil {
		t.Fatalf("EnterResult(m=%d %d-%d): %v", mid, a, b, err)
	}
}

// TestAchievementsOracleFromRealBets is the end-to-end smoke test: bets placed
// through the service, results entered, badges computed — and the card carries
// the same awards as the Trophy Room.
func TestAchievementsOracleFromRealBets(t *testing.T) {
	svc, store, fc := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")

	kicks := []time.Time{base.Add(time.Hour), base.Add(2 * time.Hour)}
	m1 := addMatch(t, store, svc.tournamentID, kicks[0])
	m2 := addMatch(t, store, svc.tournamentID, kicks[1])

	mustBet(t, svc, alice.ID, m1, 2, 1) // exact
	mustBet(t, svc, alice.ID, m2, 0, 0) // exact
	mustBet(t, svc, bob.ID, m1, 1, 0)   // right result only
	mustBet(t, svc, bob.ID, m2, 1, 1)   // right result only

	fc.T = base.Add(3 * time.Hour)
	enterResult(t, svc, admin, m1, 2, 1)
	enterResult(t, svc, admin, m2, 0, 0)

	got := boardHolders(t, svc, admin, achievements.Oracle)
	if len(got) != 1 || got[0] != "Alice" {
		t.Fatalf("Oracle holders = %v, want [Alice]", got)
	}

	// The card badge row carries the same award.
	cards, err := svc.PlayerCards(admin)
	if err != nil {
		t.Fatalf("PlayerCards: %v", err)
	}
	card := cardByName(t, cards, "Alice")
	found := false
	for _, aw := range card.Badges {
		if aw.Badge.ID == achievements.Oracle.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("Alice's card badges = %v, want The Oracle among them", card.Badges)
	}
}

// TestAchievementsTimingFromClock proves the timing badges read real bet
// timestamps under the injected clock: three last-minute picks make a Deadline
// Junkie, three placed days ahead make an Early Bird.
func TestAchievementsTimingFromClock(t *testing.T) {
	svc, store, fc := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")
	rush, _ := svc.Register("SHA256:rush", testInvite, "Rush")
	calm, _ := svc.Register("SHA256:calm", testInvite, "Calm")

	var mids []int64
	for i := 0; i < 3; i++ {
		mids = append(mids, addMatch(t, store, svc.tournamentID, base.Add(100*time.Hour+time.Duration(i)*time.Hour)))
	}

	// Calm bets right away: 100h+ before kickoff (> 48h).
	for _, mid := range mids {
		mustBet(t, svc, calm.ID, mid, 1, 0)
	}
	// Rush bets 5 minutes before each kickoff (< 10 min).
	for i, mid := range mids {
		fc.T = base.Add(100*time.Hour + time.Duration(i)*time.Hour - 5*time.Minute)
		mustBet(t, svc, rush.ID, mid, 1, 0)
	}

	fc.T = base.Add(200 * time.Hour)
	for _, mid := range mids {
		enterResult(t, svc, admin, mid, 1, 0)
	}

	if got := boardHolders(t, svc, admin, achievements.DeadlineJunkie); len(got) != 1 || got[0] != "Rush" {
		t.Fatalf("Deadline Junkie holders = %v, want [Rush]", got)
	}
	if got := boardHolders(t, svc, admin, achievements.EarlyBird); len(got) != 1 || got[0] != "Calm" {
		t.Fatalf("Early Bird holders = %v, want [Calm]", got)
	}
}

// TestAchievementsIgnoreEscapeHatchTiming: a bet UPSERTED after kickoff (the
// place-bet escape hatch / ai-seed path) must never count as a "deadline" pick —
// its created_at is meaningless for timing.
func TestAchievementsIgnoreEscapeHatchTiming(t *testing.T) {
	svc, store, fc := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")
	helped, _ := svc.Register("SHA256:helped", testInvite, "Helped")

	var mids []int64
	for i := 0; i < 3; i++ {
		mids = append(mids, addMatch(t, store, svc.tournamentID, base.Add(time.Duration(i+1)*time.Hour)))
	}

	// All three inserted well after kickoff, straight to the store — exactly
	// what `bethoven place-bet` does.
	fc.T = base.Add(48 * time.Hour)
	for _, mid := range mids {
		if err := store.UpsertBet(models.Bet{UserID: helped.ID, MatchID: mid, PredA: 1, PredB: 0}, fc.Now()); err != nil {
			t.Fatalf("UpsertBet: %v", err)
		}
		enterResult(t, svc, admin, mid, 1, 0)
	}

	if got := boardHolders(t, svc, admin, achievements.DeadlineJunkie); len(got) != 0 {
		t.Fatalf("Deadline Junkie holders = %v, want unclaimed (post-kickoff inserts)", got)
	}
}

// TestAchievementsQuitterNeedsTheBusinessEnd: the same trailing tail is a
// Quitter only once the tournament is late (here: every match finished).
func TestAchievementsQuitterNeedsTheBusinessEnd(t *testing.T) {
	svc, store, fc := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")
	ghost, _ := svc.Register("SHA256:ghost", testInvite, "Ghost")

	var mids []int64
	for i := 0; i < 4; i++ {
		mids = append(mids, addMatch(t, store, svc.tournamentID, base.Add(time.Duration(i+1)*time.Hour)))
	}
	mustBet(t, svc, ghost.ID, mids[0], 1, 0) // played once, then vanished

	// Only the first match finished (25% < defectorLateFrac): tail, but not late.
	fc.T = base.Add(10 * time.Hour)
	enterResult(t, svc, admin, mids[0], 1, 0)
	if got := boardHolders(t, svc, admin, achievements.Quitter); len(got) != 0 {
		t.Fatalf("Quitter holders = %v, want none before the business end", got)
	}

	// Everything finished: the 3-game blank tail is now a desertion.
	for _, mid := range mids[1:] {
		enterResult(t, svc, admin, mid, 2, 0)
	}
	if got := boardHolders(t, svc, admin, achievements.Quitter); len(got) != 1 || got[0] != "Ghost" {
		t.Fatalf("Quitter holders = %v, want [Ghost]", got)
	}
}

// TestAchievementsRequireAdmin: the board is an admin view — players meet their
// badges on their player card (MyCard, gated by TournamentComplete), never here.
func TestAchievementsRequireAdmin(t *testing.T) {
	svc, _, _ := newTestService(t)
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	if _, err := svc.Achievements(alice); !errors.Is(err, ErrForbidden) {
		t.Errorf("Achievements by player = %v, want ErrForbidden", err)
	}
	if _, err := svc.Achievements(nil); !errors.Is(err, ErrForbidden) {
		t.Errorf("Achievements by nil = %v, want ErrForbidden", err)
	}
}
