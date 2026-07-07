package achievements

import (
	"testing"
	"time"
)

// --- helpers -----------------------------------------------------------------

var kickoff = time.Date(2026, 6, 20, 15, 0, 0, 0, time.UTC)

// bet builds a scored pick with Classic-style points/flags derived from the
// scoreline (the tests exercise achievements, not scoring — any consistent
// points source works).
func bet(round string, predA, predB, scoreA, scoreB int) Pick {
	p := Pick{
		Round: round, PredA: predA, PredB: predB, ScoreA: scoreA, ScoreB: scoreB,
		Kickoff: kickoff, PlacedAt: kickoff.Add(-24 * time.Hour), UpdatedAt: kickoff.Add(-24 * time.Hour),
		ResultShare: -1,
	}
	p.Exact = predA == scoreA && predB == scoreB
	p.Correct = sign(predA-predB) == sign(scoreA-scoreB)
	switch {
	case p.Exact:
		p.Points = 3
	case p.Correct:
		p.Points = 1
	}
	return p
}

func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	}
	return 0
}

func skip(round string) Pick { return Pick{Round: round, Skipped: true, Kickoff: kickoff} }

// placed returns the pick with its timing set: placed lead before kickoff.
func placed(p Pick, lead time.Duration) Pick {
	p.PlacedAt = kickoff.Add(-lead)
	p.UpdatedAt = p.PlacedAt
	return p
}

// edited marks the pick as updated later (still before kickoff unless the
// offset pushes past it).
func edited(p Pick, after time.Duration) Pick {
	p.UpdatedAt = p.PlacedAt.Add(after)
	return p
}

func player(id int64, name string, picks ...Pick) PlayerInput {
	return PlayerInput{UserID: id, Name: name, Picks: picks}
}

// holders returns the holder names for one badge on the board.
func holders(t *testing.T, b Board, badge Badge) []string {
	t.Helper()
	for _, st := range b.Standings {
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

func wantHolders(t *testing.T, b Board, badge Badge, want ...string) {
	t.Helper()
	got := holders(t, b, badge)
	if len(got) != len(want) {
		t.Fatalf("%s holders = %v, want %v", badge.Name, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s holders = %v, want %v", badge.Name, got, want)
		}
	}
}

// --- superlatives ------------------------------------------------------------

func TestOracleMostExactsWinsAndMinimumGates(t *testing.T) {
	alice := player(1, "alice",
		bet("r1", 2, 1, 2, 1), bet("r2", 0, 0, 0, 0), bet("r3", 1, 3, 1, 3)) // 3 exacts
	bob := player(2, "bob",
		bet("r1", 2, 1, 2, 1), bet("r2", 1, 0, 2, 0)) // 1 exact — under minimum
	board := Compute(Input{Players: []PlayerInput{alice, bob}})
	wantHolders(t, board, Oracle, "alice")

	// Alone with a single exact, the badge stays unclaimed (minimum gate).
	board = Compute(Input{Players: []PlayerInput{bob}})
	wantHolders(t, board, Oracle)
}

func TestOracleTieShares(t *testing.T) {
	alice := player(1, "alice", bet("r1", 2, 1, 2, 1), bet("r2", 0, 0, 0, 0))
	bob := player(2, "bob", bet("r1", 1, 1, 1, 1), bet("r2", 3, 0, 3, 0))
	board := Compute(Input{Players: []PlayerInput{alice, bob}})
	wantHolders(t, board, Oracle, "alice", "bob")
	if len(board.ByUser[1]) == 0 || len(board.ByUser[2]) == 0 {
		t.Fatalf("both tied players should carry the award: %v / %v", board.ByUser[1], board.ByUser[2])
	}
}

func TestLongestStreakSkipBreaksRun(t *testing.T) {
	// 3 correct in a row, then a skip, then 2 correct: best streak 3.
	alice := player(1, "alice",
		bet("r1", 1, 0, 2, 0), bet("r1", 2, 1, 1, 0), bet("r2", 0, 0, 1, 1),
		skip("r2"),
		bet("r3", 1, 0, 3, 1), bet("r3", 2, 0, 1, 0))
	// 4 correct but a MISS in the middle: best streak 2 — under minimum.
	bob := player(2, "bob",
		bet("r1", 1, 0, 2, 0), bet("r1", 1, 0, 1, 0),
		bet("r2", 1, 0, 0, 2), // wrong result
		bet("r2", 1, 0, 1, 0), bet("r3", 1, 0, 2, 1))
	board := Compute(Input{Players: []PlayerInput{alice, bob}})
	wantHolders(t, board, LongestStreak, "alice")
}

func TestTopRoundFromTrajectory(t *testing.T) {
	alice := PlayerInput{UserID: 1, Name: "alice", Rounds: []RoundDelta{
		{Label: "r1", Position: 2, PointsGained: 4},
		{Label: "r2", Position: 1, PointsGained: 9},
	}}
	bob := PlayerInput{UserID: 2, Name: "bob", Rounds: []RoundDelta{
		{Label: "r1", Position: 1, PointsGained: 7},
		{Label: "r2", Position: 2, PointsGained: 0},
	}}
	board := Compute(Input{Players: []PlayerInput{alice, bob}})
	wantHolders(t, board, TopRound, "alice")

	// A pointless history holds nothing.
	blank := PlayerInput{UserID: 3, Name: "carol", Rounds: []RoundDelta{{Label: "r1", Position: 3}}}
	board = Compute(Input{Players: []PlayerInput{blank}})
	wantHolders(t, board, TopRound)
}

func TestComebackAndFreeFallSkipFirstRoundAndGateSmallMoves(t *testing.T) {
	// First-round movement (the join jump) must not count.
	joiner := PlayerInput{UserID: 1, Name: "joiner", Rounds: []RoundDelta{
		{Label: "r2", Position: 2, Movement: 5}, // artificial join jump
		{Label: "r3", Position: 1, Movement: 1}, // real, but under minMovement
	}}
	climber := PlayerInput{UserID: 2, Name: "climber", Rounds: []RoundDelta{
		{Label: "r1", Position: 6},
		{Label: "r2", Position: 3, Movement: 3},
		{Label: "r3", Position: 5, Movement: -2},
	}}
	board := Compute(Input{Players: []PlayerInput{joiner, climber}})
	wantHolders(t, board, Comeback, "climber")
	wantHolders(t, board, FreeFall, "climber")
}

func TestDrawWhisperer(t *testing.T) {
	// Two correct draw calls (one exact, one right-result 1-1 vs 2-2) qualify.
	alice := player(1, "alice",
		bet("r1", 0, 0, 0, 0), bet("r2", 1, 1, 2, 2), bet("r3", 2, 0, 2, 0))
	// Predicting draws that don't land earns nothing.
	bob := player(2, "bob", bet("r1", 1, 1, 2, 0), bet("r2", 0, 0, 1, 0))
	board := Compute(Input{Players: []PlayerInput{alice, bob}})
	wantHolders(t, board, DrawWhisperer, "alice")
}

func TestGoalMerchantAndAccountantNeedFivePicks(t *testing.T) {
	heavy := player(1, "heavy",
		bet("r1", 3, 2, 1, 0), bet("r1", 4, 1, 0, 0), bet("r2", 2, 2, 1, 1),
		bet("r2", 3, 3, 2, 0), bet("r3", 5, 0, 1, 0))
	light := player(2, "light",
		bet("r1", 1, 0, 1, 0), bet("r1", 0, 0, 0, 0), bet("r2", 1, 0, 2, 1),
		bet("r2", 2, 0, 1, 1), bet("r3", 1, 1, 0, 0))
	four := player(3, "four", // only 4 picks — ineligible for either
		bet("r1", 9, 9, 1, 0), bet("r1", 0, 0, 1, 0), bet("r2", 0, 0, 2, 0), bet("r2", 0, 0, 0, 2))
	board := Compute(Input{Players: []PlayerInput{heavy, light, four}})
	wantHolders(t, board, GoalMerchant, "heavy")
	wantHolders(t, board, Accountant, "light")
}

func TestTimingBadges(t *testing.T) {
	late := player(1, "late",
		placed(bet("r1", 1, 0, 1, 0), 5*time.Minute),
		placed(bet("r1", 2, 0, 2, 0), 2*time.Minute),
		placed(bet("r2", 1, 1, 1, 1), 9*time.Minute))
	early := player(2, "early",
		placed(bet("r1", 1, 0, 1, 0), 72*time.Hour),
		placed(bet("r1", 2, 0, 2, 0), 96*time.Hour),
		placed(bet("r2", 1, 1, 1, 1), 49*time.Hour))
	board := Compute(Input{Players: []PlayerInput{late, early}})
	wantHolders(t, board, DeadlineJunkie, "late")
	wantHolders(t, board, EarlyBird, "early")
}

func TestTimingExcludesPostKickoffInsertsAndAI(t *testing.T) {
	// Three "late" picks, but placed AFTER kickoff — the place-bet escape hatch
	// or ai-seed. No timing badge.
	hatch := player(1, "hatch",
		placed(bet("r1", 1, 0, 1, 0), -time.Minute),
		placed(bet("r1", 2, 0, 2, 0), -time.Hour),
		placed(bet("r2", 1, 1, 1, 1), -time.Second))
	board := Compute(Input{Players: []PlayerInput{hatch}})
	wantHolders(t, board, DeadlineJunkie)

	// BETanIA never holds a timing badge even with valid-looking timestamps.
	betania := player(2, "BETanIA 🤖",
		placed(bet("r1", 1, 0, 1, 0), time.Minute),
		placed(bet("r1", 2, 0, 2, 0), time.Minute),
		placed(bet("r2", 1, 1, 1, 1), time.Minute))
	betania.IsAI = true
	board = Compute(Input{Players: []PlayerInput{betania}})
	wantHolders(t, board, DeadlineJunkie)
}

func TestSecondGuesserCountsPreKickoffEditsOnly(t *testing.T) {
	waverer := player(1, "waverer",
		edited(placed(bet("r1", 1, 0, 1, 0), time.Hour), 10*time.Minute),
		edited(placed(bet("r1", 2, 0, 2, 0), time.Hour), 30*time.Minute),
		edited(placed(bet("r2", 1, 1, 1, 1), time.Hour), 5*time.Minute))
	// Edits landing after kickoff (escape-hatch corrections) don't count.
	corrected := player(2, "corrected",
		edited(placed(bet("r1", 1, 0, 1, 0), time.Hour), 2*time.Hour),
		edited(placed(bet("r1", 2, 0, 2, 0), time.Hour), 3*time.Hour),
		edited(placed(bet("r2", 1, 1, 1, 1), time.Hour), 4*time.Hour))
	board := Compute(Input{Players: []PlayerInput{waverer, corrected}})
	wantHolders(t, board, SecondGuesser, "waverer")
}

func TestContrarianNeedsQuorumAndPoints(t *testing.T) {
	rare := player(1, "rare",
		bet("r1", 2, 1, 2, 1), bet("r2", 1, 0, 1, 0))
	rare.Picks[0].ResultShare = 0.2 // rare and scored
	rare.Picks[1].ResultShare = 0.6 // popular — no credit

	underQuorum := player(2, "underq", bet("r1", 2, 1, 2, 1))
	underQuorum.Picks[0].ResultShare = -1 // match below quorum

	missed := player(3, "missed", bet("r1", 2, 1, 0, 3))
	missed.Picks[0].ResultShare = 0.1 // rare but scored nothing

	board := Compute(Input{Players: []PlayerInput{rare, underQuorum, missed}})
	wantHolders(t, board, Contrarian, "rare")
}

// --- thresholds ----------------------------------------------------------------

func TestPerfectRoundAndBlackoutNeedThreePicks(t *testing.T) {
	perfect := player(1, "perfect",
		bet("r1", 1, 0, 1, 0), bet("r1", 2, 1, 2, 1), bet("r1", 0, 0, 1, 1))
	blackout := player(2, "blackout",
		bet("r1", 1, 0, 0, 1), bet("r1", 2, 1, 0, 0), bet("r1", 0, 0, 2, 0))
	twoOnly := player(3, "two", // perfect but only 2 picks that round
		bet("r1", 1, 0, 1, 0), bet("r1", 2, 1, 2, 1))
	board := Compute(Input{Players: []PlayerInput{perfect, blackout, twoOnly}})
	wantHolders(t, board, PerfectRound, "perfect")
	wantHolders(t, board, Blackout, "blackout")
}

func TestHotHandNeedsConsecutiveExacts(t *testing.T) {
	hot := player(1, "hot",
		bet("r1", 1, 0, 1, 0), bet("r1", 2, 2, 2, 2))
	// Two exacts split by a miss: no Hot Hand.
	cold := player(2, "cold",
		bet("r1", 1, 0, 1, 0), bet("r1", 0, 0, 3, 0), bet("r2", 2, 2, 2, 2))
	board := Compute(Input{Players: []PlayerInput{hot, cold}})
	wantHolders(t, board, HotHand, "hot")
}

func TestEverPresent(t *testing.T) {
	full := PlayerInput{UserID: 1, Name: "full", Part: Participation{Available: 12, Bet: 12}}
	gap := PlayerInput{UserID: 2, Name: "gap", Part: Participation{Available: 12, Bet: 11}}
	early := PlayerInput{UserID: 3, Name: "early", Part: Participation{Available: 6, Bet: 6}} // under minimum
	board := Compute(Input{Players: []PlayerInput{full, gap, early}})
	wantHolders(t, board, EverPresent, "full")
}

func TestQuitterOnlyInTheBusinessEnd(t *testing.T) {
	ghost := PlayerInput{UserID: 1, Name: "ghost", Part: Participation{Available: 10, Bet: 4, RecentSkips: 5}}
	neverStarted := PlayerInput{UserID: 2, Name: "never", Part: Participation{Available: 10, Bet: 0, RecentSkips: 10}}

	board := Compute(Input{Players: []PlayerInput{ghost, neverStarted}, TournamentLate: true})
	wantHolders(t, board, Quitter, "ghost") // a never-start sits out, not quits

	// Same tail early in the tournament is a blip, not desertion.
	board = Compute(Input{Players: []PlayerInput{ghost}, TournamentLate: false})
	wantHolders(t, board, Quitter)
}

func TestWireToWire(t *testing.T) {
	lead := func(n int) []RoundDelta {
		rs := make([]RoundDelta, n)
		for i := range rs {
			rs[i] = RoundDelta{Label: "r", Position: 1}
		}
		return rs
	}
	train := PlayerInput{UserID: 1, Name: "train", Rounds: lead(5)}
	short := PlayerInput{UserID: 2, Name: "short", Rounds: lead(4)} // under minimum
	slipped := PlayerInput{UserID: 3, Name: "slipped", Rounds: append(lead(4), RoundDelta{Position: 2})}
	board := Compute(Input{Players: []PlayerInput{train, short, slipped}})
	wantHolders(t, board, WireToWire, "train")
}

// --- board shape -----------------------------------------------------------------

func TestBoardListsWholeCatalogAndByUserMatchesStandings(t *testing.T) {
	alice := player(1, "alice",
		bet("r1", 2, 1, 2, 1), bet("r2", 0, 0, 0, 0))
	board := Compute(Input{Players: []PlayerInput{alice}})

	if len(board.Standings) != len(Catalog) {
		t.Fatalf("standings rows = %d, want the whole catalog (%d)", len(board.Standings), len(Catalog))
	}
	for i, st := range board.Standings {
		if st.Badge.ID != Catalog[i].ID {
			t.Fatalf("standings[%d] = %s, want catalog order (%s)", i, st.Badge.ID, Catalog[i].ID)
		}
	}
	// Every holder on the standings appears in ByUser and vice versa.
	total := 0
	for _, st := range board.Standings {
		total += len(st.Holders)
	}
	byUser := 0
	for _, aws := range board.ByUser {
		byUser += len(aws)
	}
	if total != byUser {
		t.Fatalf("standings holders (%d) != ByUser awards (%d)", total, byUser)
	}
}

func TestEmptyPoolIsAllUnclaimed(t *testing.T) {
	board := Compute(Input{})
	if len(board.Standings) != len(Catalog) {
		t.Fatalf("standings rows = %d, want %d", len(board.Standings), len(Catalog))
	}
	for _, st := range board.Standings {
		if len(st.Holders) != 0 {
			t.Fatalf("%s claimed on an empty pool", st.Badge.Name)
		}
	}
}
