package scoring

import (
	"testing"

	"bethoven/internal/models"
)

func TestProximityPoints(t *testing.T) {
	tests := []struct {
		name         string
		predA, predB int
		match        models.Match
		want         int
	}{
		// exact -> 5
		{"exact home win", 1, 0, finished(1, 0), 5},
		{"exact draw", 2, 2, finished(2, 2), 5},
		{"exact 0-0", 0, 0, finished(0, 0), 5},

		// the headline case: 4-1 game, closer correct guess scores more
		{"4-1: bet 4-1 exact", 4, 1, finished(4, 1), 5},
		{"4-1: bet 3-1 (1 off)", 3, 1, finished(4, 1), 4},
		{"4-1: bet 5-2 (2 off)", 5, 2, finished(4, 1), 3},
		{"4-1: bet 2-0 (3 off)", 2, 0, finished(4, 1), 2},

		// correct result but far away -> floor of 1
		{"home win, far off", 1, 0, finished(9, 0), 1},
		{"draw, far off", 4, 4, finished(0, 0), 1},

		// wrong result -> 0
		{"picked away, was home win", 0, 1, finished(2, 1), 0},
		{"picked draw, was home win", 1, 1, finished(2, 0), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bet := models.Bet{PredA: tc.predA, PredB: tc.predB}
			if got := ProximityPoints(bet, tc.match); got != tc.want {
				t.Errorf("ProximityPoints(%d-%d vs %d-%d) = %d, want %d",
					tc.predA, tc.predB, *tc.match.ScoreA, *tc.match.ScoreB, got, tc.want)
			}
		})
	}
}

func TestProximityUnfinishedIsZero(t *testing.T) {
	bet := models.Bet{PredA: 2, PredB: 1}
	if got := ProximityPoints(bet, models.Match{Finished: false}); got != 0 {
		t.Errorf("unfinished match should score 0, got %d", got)
	}
}

func TestProximityKnockoutDraw(t *testing.T) {
	// 1-1 a.e.t. stored as regulation 1-1; predicting 1-1 is exact -> 5.
	bet := models.Bet{PredA: 1, PredB: 1}
	if got := ProximityPoints(bet, finished(1, 1)); got != 5 {
		t.Errorf("exact 1-1 should score 5, got %d", got)
	}
}

func TestScarcityPoints(t *testing.T) {
	tests := []struct {
		name         string
		predA, predB int
		match        models.Match
		pool         Pool
		want         int
	}{
		// Proximity base + bonuses. Base for a 4-1 game:
		//   exact 4-1 -> 5, 3-1 -> 4, far correct -> 1.

		// Rare exact pick: 1 of 20 got the result, 1 of 20 got the exact (5% < 10%)
		// -> base 5 + result bonus 2 + exact bonus 2 = 9.
		{"rare exact", 4, 1, finished(4, 1),
			Pool{Total: 20, SameResult: 1, SameExact: 1}, 9},

		// Exact-bonus boundary: 1 of 10 is exactly 10%, not strictly below, so the
		// exact bonus does NOT fire. Result bonus still does (1/10 < 25%).
		// base 5 + result 2 = 7.
		{"exact at 10pct boundary", 4, 1, finished(4, 1),
			Pool{Total: 10, SameResult: 1, SameExact: 1}, 7},

		// Rare result, common-ish exact (exact threshold not met): base 4 +
		// result bonus 2 = 6. SameExact 2/10 = 20% >= 10% so no exact bonus
		// (and it's not exact anyway).
		{"rare result, not exact", 3, 1, finished(4, 1),
			Pool{Total: 10, SameResult: 2, SameExact: 2}, 6},

		// Popular result: 5 of 10 got it -> no bonus. base 4.
		{"popular result", 3, 1, finished(4, 1),
			Pool{Total: 10, SameResult: 5, SameExact: 1}, 4},

		// Exact but everyone got it: result 8/10 and exact 8/10 -> no bonus. base 5.
		{"popular exact", 4, 1, finished(4, 1),
			Pool{Total: 10, SameResult: 8, SameExact: 8}, 5},

		// Wrong result: never any bonus, base 0.
		{"wrong result, rare", 0, 1, finished(2, 1),
			Pool{Total: 10, SameResult: 1, SameExact: 1}, 0},

		// Boundary: exactly 25% result share does NOT qualify (strict <).
		{"result at 25pct boundary", 3, 1, finished(4, 1),
			Pool{Total: 8, SameResult: 2, SameExact: 1}, 4},

		// Empty pool is safe: no bonus, just base.
		{"empty pool", 4, 1, finished(4, 1),
			Pool{}, 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bet := models.Bet{PredA: tc.predA, PredB: tc.predB}
			if got := ScarcityPoints(bet, tc.match, tc.pool); got != tc.want {
				t.Errorf("ScarcityPoints(%d-%d vs %d-%d, %+v) = %d, want %d",
					tc.predA, tc.predB, *tc.match.ScoreA, *tc.match.ScoreB, tc.pool, got, tc.want)
			}
		})
	}
}

func TestModeRoundTrip(t *testing.T) {
	for _, m := range []Mode{ModeClassic, ModeProximity, ModeScarcity} {
		if got := ParseMode(m.String()); got != m {
			t.Errorf("ParseMode(%q) = %v, want %v", m.String(), got, m)
		}
	}
	if ParseMode("") != ModeClassic {
		t.Errorf("empty string should parse to Classic")
	}
	if ParseMode("bogus") != ModeClassic {
		t.Errorf("unknown string should parse to Classic")
	}
}

func TestScoreDispatch(t *testing.T) {
	// 4-1 game, bet 3-1: Classic=1, Proximity=4, Scarcity=4+2 (rare result).
	bet := models.Bet{PredA: 3, PredB: 1}
	m := finished(4, 1)
	if got := Score(ModeClassic, bet, m, Pool{}); got != 1 {
		t.Errorf("Classic dispatch = %d, want 1", got)
	}
	if got := Score(ModeProximity, bet, m, Pool{}); got != 4 {
		t.Errorf("Proximity dispatch = %d, want 4", got)
	}
	if got := Score(ModeScarcity, bet, m, Pool{Total: 10, SameResult: 1}); got != 6 {
		t.Errorf("Scarcity dispatch = %d, want 6", got)
	}
}

func TestResult(t *testing.T) {
	cases := []struct {
		a, b int
		want int
	}{{2, 0, 1}, {0, 2, -1}, {1, 1, 0}}
	for _, c := range cases {
		if got := Result(models.Bet{PredA: c.a, PredB: c.b}); got != c.want {
			t.Errorf("Result(%d-%d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
