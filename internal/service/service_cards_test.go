package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"bethoven/internal/ai"
	"bethoven/internal/models"
)

// cardByName finds a player's card in the ranked slice, failing if absent.
func cardByName(t *testing.T, cards []PlayerCard, name string) PlayerCard {
	t.Helper()
	for _, c := range cards {
		if c.User.DisplayName == name {
			return c
		}
	}
	t.Fatalf("no card for %q", name)
	return PlayerCard{}
}

// seedTwoRoundPool sets up the same two-round, two-player pool the standings-history
// test uses, returning the service + the two players. Final: Bob 4 (#1), Alice 3 (#2).
func seedTwoRoundPool(t *testing.T) (*Service, models.User, models.User, models.User) {
	t.Helper()
	svc, store, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, testInvite, "Admin")
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")

	day1 := base.Add(2 * time.Hour)  // 2026-06-11
	day2 := base.Add(26 * time.Hour) // 2026-06-12
	m1 := addMatch(t, store, svc.tournamentID, day1)
	m2 := addMatch(t, store, svc.tournamentID, day2)

	betOK(t, svc, alice.ID, m1, 2, 1) // exact
	betOK(t, svc, bob.ID, m1, 1, 0)   // right result only
	betOK(t, svc, alice.ID, m2, 1, 1) // wrong (a miss)
	betOK(t, svc, bob.ID, m2, 0, 3)   // exact

	if err := svc.EnterResult(admin, m1, 2, 1); err != nil {
		t.Fatalf("EnterResult m1: %v", err)
	}
	if err := svc.EnterResult(admin, m2, 0, 3); err != nil {
		t.Fatalf("EnterResult m2: %v", err)
	}
	return svc, *admin, *alice, *bob
}

// PlayerCards folds the standings history into a card per player: final rank, medal,
// tallies, trajectory extremes, and best/worst pick — all read-time, no new storage.
func TestPlayerCardsComputesStats(t *testing.T) {
	svc, admin, _, _ := seedTwoRoundPool(t)

	cards, err := svc.PlayerCards(&admin)
	if err != nil {
		t.Fatalf("PlayerCards: %v", err)
	}
	// Bob, Alice, and the (non-betting) Admin all appear — the card set mirrors the
	// leaderboard, which lists every registered user.
	if len(cards) != 3 {
		t.Fatalf("expected 3 cards, got %d", len(cards))
	}
	// Ranked: champion first.
	if cards[0].User.DisplayName != "Bob" || cards[0].FinalRank != 1 || cards[0].Medal != 1 {
		t.Errorf("cards[0] should be Bob #1 medal 1, got %+v", cards[0])
	}

	bob := cardByName(t, cards, "Bob")
	if bob.Total != 4 || bob.ExactHits != 1 || bob.CorrectResults != 2 {
		t.Errorf("Bob stats = total %d exact %d correct %d; want 4/1/2", bob.Total, bob.ExactHits, bob.CorrectResults)
	}
	if bob.StartRank != 2 || bob.PeakRank != 1 || bob.BiggestClimb != 1 {
		t.Errorf("Bob arc = start %d peak %d climb %d; want 2/1/1", bob.StartRank, bob.PeakRank, bob.BiggestClimb)
	}
	if bob.BestPick == nil || bob.BestPick.Points != 3 {
		t.Errorf("Bob best pick = %+v; want a 3-pointer", bob.BestPick)
	}
	if bob.WorstPick != nil {
		t.Errorf("Bob never missed a finished bet, want nil WorstPick, got %+v", bob.WorstPick)
	}
	// Deterministic card metrics: Bob got both his bets right (streak 2, 100% hit
	// rate) and led after round 2 (1 round at #1).
	if bob.BestStreak != 2 || bob.HitRate != 100 || bob.RoundsAsLeader != 1 {
		t.Errorf("Bob metrics = streak %d hit %d leader %d; want 2/100/1", bob.BestStreak, bob.HitRate, bob.RoundsAsLeader)
	}

	alice := cardByName(t, cards, "Alice")
	if alice.FinalRank != 2 || alice.Medal != 2 {
		t.Errorf("Alice should finish #2 (medal 2), got rank %d medal %d", alice.FinalRank, alice.Medal)
	}
	if alice.Total != 3 || alice.ExactHits != 1 || alice.CorrectResults != 1 {
		t.Errorf("Alice stats = total %d exact %d correct %d; want 3/1/1", alice.Total, alice.ExactHits, alice.CorrectResults)
	}
	if alice.PeakRank != 1 {
		t.Errorf("Alice peaked at #1, got %d", alice.PeakRank)
	}
	if alice.BestPick == nil || alice.BestPick.Points != 3 {
		t.Errorf("Alice best pick = %+v; want her exact 3-pointer", alice.BestPick)
	}
	if alice.WorstPick == nil || alice.WorstPick.Points != 0 {
		t.Errorf("Alice missed match 2, want a 0-point WorstPick, got %+v", alice.WorstPick)
	}
	if len(alice.Trajectory) != 2 {
		t.Errorf("expected a 2-round trajectory, got %d", len(alice.Trajectory))
	}
	// Alice nailed round 1 then missed round 2: streak 1, 1-of-2 = 50% hit rate, and
	// she sat at #1 for one round before Bob overtook her.
	if alice.BestStreak != 1 || alice.HitRate != 50 || alice.RoundsAsLeader != 1 {
		t.Errorf("Alice metrics = streak %d hit %d leader %d; want 1/50/1", alice.BestStreak, alice.HitRate, alice.RoundsAsLeader)
	}
}

// A card must distinguish a NO-PICK from a wrong pick, respect when a player joined,
// and surface a give-up tail. Alice plays from the start then quits; Zoe joins after
// the first match and quits before the last.
func TestPlayerCardsParticipationAndTenure(t *testing.T) {
	svc, store, fc := newTestService(t)
	admin, _ := svc.Register(adminFP, testInvite, "Admin")
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")

	m1 := addMatch(t, store, svc.tournamentID, base.Add(2*time.Hour))  // 06-11
	m2 := addMatch(t, store, svc.tournamentID, base.Add(26*time.Hour)) // 06-12
	m3 := addMatch(t, store, svc.tournamentID, base.Add(50*time.Hour)) // 06-13
	m4 := addMatch(t, store, svc.tournamentID, base.Add(74*time.Hour)) // 06-14

	// Alice (registered at base) bets the first two, then gives up.
	betOK(t, svc, alice.ID, m1, 1, 0)
	betOK(t, svc, alice.ID, m2, 2, 2)

	// Zoe joins after m1 has kicked off (joined late), bets m2 and m3, skips m4.
	fc.T = base.Add(3 * time.Hour)
	zoe, _ := svc.Register("SHA256:zoe", testInvite, "Zoe")
	betOK(t, svc, zoe.ID, m2, 0, 0)
	betOK(t, svc, zoe.ID, m3, 1, 1)

	// Settle everything (clock well past the last kickoff).
	fc.T = base.Add(200 * time.Hour)
	for _, m := range []int64{m1, m2, m3, m4} {
		if err := svc.EnterResult(admin, m, 1, 1); err != nil {
			t.Fatalf("EnterResult m%d: %v", m, err)
		}
	}

	cards, err := svc.PlayerCards(admin)
	if err != nil {
		t.Fatalf("PlayerCards: %v", err)
	}

	a := cardByName(t, cards, "Alice")
	if a.JoinedLate {
		t.Error("Alice registered before any match — not a late joiner")
	}
	if a.MatchesAvailable != 4 || a.MatchesBet != 2 || a.MatchesSkipped != 2 {
		t.Errorf("Alice participation = avail %d bet %d skip %d; want 4/2/2", a.MatchesAvailable, a.MatchesBet, a.MatchesSkipped)
	}
	if a.MatchesBeforeJoining != 0 {
		t.Errorf("Alice before-joining = %d; want 0", a.MatchesBeforeJoining)
	}
	if a.RecentSkips != 2 { // m3, m4 left blank after her last pick (m2)
		t.Errorf("Alice recent skips = %d; want 2 (gave up after m2)", a.RecentSkips)
	}

	z := cardByName(t, cards, "Zoe")
	if !z.JoinedLate || z.MatchesBeforeJoining != 1 {
		t.Errorf("Zoe should be a late joiner with 1 pre-join match, got late=%v before=%d", z.JoinedLate, z.MatchesBeforeJoining)
	}
	if z.MatchesAvailable != 3 || z.MatchesBet != 2 || z.MatchesSkipped != 1 {
		t.Errorf("Zoe participation = avail %d bet %d skip %d; want 3/2/1", z.MatchesAvailable, z.MatchesBet, z.MatchesSkipped)
	}
	if z.RecentSkips != 1 { // m4 left blank after her last pick (m3)
		t.Errorf("Zoe recent skips = %d; want 1", z.RecentSkips)
	}

	// The digest carries these so BETanIA never reads a skip as a wrong pick.
	d, err := svc.CardDigest(zoe.ID)
	if err != nil {
		t.Fatalf("CardDigest: %v", err)
	}
	if d.MatchesSkipped != 1 || !d.JoinedLate || d.RegisteredAt == "" || d.RecentSkips != 1 {
		t.Errorf("Zoe digest = %+v; want skipped 1, late, a reg date, recent_skips 1", d)
	}
}

// CommentConfig carries per-player participation grounding so BETanIA's roasts and
// live commentary never read a no-pick as a wrong pick — but ONLY for players with a
// caveat (a skip, a late join, or never picking). Mirrors the card-tenure test setup.
func TestCommentConfigParticipation(t *testing.T) {
	svc, store, fc := newTestService(t)
	admin, _ := svc.Register(adminFP, testInvite, "Admin")
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")

	m1 := addMatch(t, store, svc.tournamentID, base.Add(2*time.Hour))  // 06-11
	m2 := addMatch(t, store, svc.tournamentID, base.Add(26*time.Hour)) // 06-12
	m3 := addMatch(t, store, svc.tournamentID, base.Add(50*time.Hour)) // 06-13

	// Bob bets every game — fully participating, so he must NOT appear in the digest.
	betOK(t, svc, bob.ID, m1, 1, 0)
	betOK(t, svc, bob.ID, m2, 1, 0)
	betOK(t, svc, bob.ID, m3, 1, 0)
	// Alice bets only the first, then goes quiet.
	betOK(t, svc, alice.ID, m1, 2, 2)

	// Zoe joins after m1 kicked off (late) and never picks at all.
	fc.T = base.Add(3 * time.Hour)
	svc.Register("SHA256:zoe", testInvite, "Zoe")

	fc.T = base.Add(200 * time.Hour)
	for _, m := range []int64{m1, m2, m3} {
		if err := svc.EnterResult(admin, m, 1, 1); err != nil {
			t.Fatalf("EnterResult: %v", err)
		}
	}

	parts := svc.CommentConfig().Participation
	byName := map[string]ai.PlayerParticipation{}
	for _, p := range parts {
		byName[p.Name] = p
	}
	if _, ok := byName["Bob"]; ok {
		t.Error("Bob bet every game — he must not appear in the participation digest")
	}
	a, ok := byName["Alice"]
	if !ok {
		t.Fatal("Alice skipped games — she must appear in the digest")
	}
	if a.MatchesAvailable != 3 || a.MatchesBet != 1 || a.MatchesSkipped != 2 || a.RecentSkips != 2 || a.JoinedLate {
		t.Errorf("Alice digest = %+v; want avail 3 bet 1 skip 2 recent 2, not late", a)
	}
	z, ok := byName["Zoe"]
	if !ok {
		t.Fatal("Zoe (late joiner, never picked) must appear in the digest")
	}
	if !z.JoinedLate || !z.NeverPicked || z.MatchesBeforeJoining != 1 || z.RegisteredAt == "" {
		t.Errorf("Zoe digest = %+v; want late, never picked, 1 before joining, a reg date", z)
	}
}

// Cards (and the generate actions) are admin-only.
func TestPlayerCardsRequireAdmin(t *testing.T) {
	svc, _, alice, _ := seedTwoRoundPool(t)
	if _, err := svc.PlayerCards(&alice); !errors.Is(err, ErrForbidden) {
		t.Errorf("PlayerCards by player = %v, want ErrForbidden", err)
	}
	if err := svc.GeneratePlayerCards(&alice); !errors.Is(err, ErrForbidden) {
		t.Errorf("GeneratePlayerCards by player = %v, want ErrForbidden", err)
	}
	if _, err := svc.RegeneratePlayerCard(&alice, alice.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("RegeneratePlayerCard by player = %v, want ErrForbidden", err)
	}
}

// The narrative is the only persisted part of a card; it round-trips through the DB.
func TestPlayerCardNarrativePersists(t *testing.T) {
	svc, admin, _, bob := seedTwoRoundPool(t)

	if err := svc.SavePlayerCardNarrative(bob.ID, "You climbed from nowhere to the crown."); err != nil {
		t.Fatalf("SavePlayerCardNarrative: %v", err)
	}
	cards, err := svc.PlayerCards(&admin)
	if err != nil {
		t.Fatalf("PlayerCards: %v", err)
	}
	got := cardByName(t, cards, "Bob")
	if got.Narrative != "You climbed from nowhere to the crown." {
		t.Errorf("narrative not overlaid: %q", got.Narrative)
	}
	if got.NarratedAt.IsZero() {
		t.Error("NarratedAt should be stamped")
	}
	// Alice has no stored narrative.
	if a := cardByName(t, cards, "Alice"); a.Narrative != "" {
		t.Errorf("Alice should have no narrative, got %q", a.Narrative)
	}
}

// CardDigests skips muted players (no tokens spent), but their stats card still
// renders; CardDigest for a muted player errors.
func TestCardDigestsSkipsMuted(t *testing.T) {
	svc, admin, alice, _ := seedTwoRoundPool(t)
	if err := svc.SetUserCommentTone(&admin, alice.ID, "mute"); err != nil {
		t.Fatalf("mute Alice: %v", err)
	}

	digests, err := svc.CardDigests()
	if err != nil {
		t.Fatalf("CardDigests: %v", err)
	}
	for _, d := range digests {
		if d.Player == "Alice" {
			t.Fatalf("muted Alice should be skipped in CardDigests, got %+v", d)
		}
	}
	// Bob + the non-betting Admin remain (Alice muted out).
	if len(digests) != 2 {
		t.Fatalf("expected 2 digests (Bob, Admin), got %d", len(digests))
	}
	if d := digests[0]; d.TotalPlayers != 3 || d.FinalRank != 1 || d.Player != "Bob" {
		t.Errorf("digests[0] = %+v; want Bob, 3 players, rank 1", d)
	}

	// The muted player still gets a stats card on the admin board.
	cards, _ := svc.PlayerCards(&admin)
	if a := cardByName(t, cards, "Alice"); a.FinalRank != 2 {
		t.Errorf("muted Alice should still have a stats card, got %+v", a)
	}
	// But a per-card digest for her errors (no narrative anywhere).
	if _, err := svc.CardDigest(alice.ID); err == nil {
		t.Error("CardDigest for a muted player should error")
	}
}

// With no comment worker attached, the generate actions report the worker is off.
func TestGeneratePlayerCardsWorkerOff(t *testing.T) {
	svc, admin, _, bob := seedTwoRoundPool(t)
	if err := svc.GeneratePlayerCards(&admin); !errors.Is(err, ErrAIOff) {
		t.Errorf("GeneratePlayerCards with no worker = %v, want ErrAIOff", err)
	}
	if _, err := svc.RegeneratePlayerCard(&admin, bob.ID); !errors.Is(err, ErrAIOff) {
		t.Errorf("RegeneratePlayerCard with no worker = %v, want ErrAIOff", err)
	}
}

// The SetCardGen seam wires the worker hooks; the admin actions then drive them.
func TestGeneratePlayerCardsViaSeam(t *testing.T) {
	svc, admin, _, bob := seedTwoRoundPool(t)
	allCalls := 0
	var oneID int64
	svc.SetCardGen(
		func(ctx context.Context) error { allCalls++; return nil },
		func(ctx context.Context, userID int64) (string, error) { oneID = userID; return "fresh card", nil },
	)
	if err := svc.GeneratePlayerCards(&admin); err != nil {
		t.Fatalf("GeneratePlayerCards: %v", err)
	}
	if allCalls != 1 {
		t.Errorf("generate-all seam calls = %d, want 1", allCalls)
	}
	txt, err := svc.RegeneratePlayerCard(&admin, bob.ID)
	if err != nil {
		t.Fatalf("RegeneratePlayerCard: %v", err)
	}
	if txt != "fresh card" || oneID != bob.ID {
		t.Errorf("regen seam = %q for id %d; want \"fresh card\" for %d", txt, oneID, bob.ID)
	}
}

// No finished matches ⇒ an empty card set (graceful, not an error).
func TestPlayerCardsEmptyBeforeAnyResult(t *testing.T) {
	svc, store, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, testInvite, "Admin")
	addMatch(t, store, svc.tournamentID, base.Add(2*time.Hour))

	cards, err := svc.PlayerCards(admin)
	if err != nil {
		t.Fatalf("PlayerCards: %v", err)
	}
	if len(cards) != 0 {
		t.Errorf("expected no cards before any result, got %d", len(cards))
	}
}
