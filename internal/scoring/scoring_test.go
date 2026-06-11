package scoring

import (
	"testing"

	"bethoven/internal/models"
)

func finished(a, b int) models.Match {
	return models.Match{Finished: true, ScoreA: &a, ScoreB: &b}
}

func TestPoints(t *testing.T) {
	tests := []struct {
		name      string
		predA     int
		predB     int
		bonusOver bool
		match     models.Match
		want      int
	}{
		// exact scores (3) + bonus correctness (+1)
		{"exact, under correct", 1, 0, false, finished(1, 0), 4}, // 1 goal -> under; predicted under
		{"exact, over correct", 2, 1, true, finished(2, 1), 4},   // 3 goals -> over; predicted over
		{"exact, bonus wrong", 2, 1, false, finished(2, 1), 3},   // 3 goals over, predicted under
		{"exact draw", 1, 1, true, finished(1, 1), 3},            // 2 goals -> under; predicted over (no bonus)

		// correct result only (1) + bonus
		{"result only, bonus correct", 3, 0, true, finished(2, 1), 2}, // home win both; 3 goals over, predicted over
		{"result only, bonus wrong", 1, 0, false, finished(3, 0), 1},  // home win; 3 goals over but predicted under
		{"draw result, bonus wrong", 2, 2, true, finished(1, 1), 1},   // draw both; 2 goals under but predicted over

		// wrong result
		{"wrong result, bonus saves a point", 0, 1, true, finished(2, 1), 1}, // away pick vs home win; 3 goals over, predicted over
		{"all wrong", 0, 2, false, finished(3, 0), 0},                        // away pick vs home win; 3 over, predicted under

		// boundary: exactly 2 goals is UNDER 2.5
		{"two goals is under", 0, 0, false, finished(2, 0), 1}, // wrong result(?) 2-0 home, pred 0-0 draw -> wrong result; under correct -> +1
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bet := models.Bet{PredA: tc.predA, PredB: tc.predB, BonusOver: tc.bonusOver}
			if got := Points(bet, tc.match); got != tc.want {
				t.Errorf("Points(%d-%d over=%v vs %d-%d) = %d, want %d",
					tc.predA, tc.predB, tc.bonusOver, *tc.match.ScoreA, *tc.match.ScoreB, got, tc.want)
			}
		})
	}
}

func TestPointsUnfinishedIsZero(t *testing.T) {
	bet := models.Bet{PredA: 2, PredB: 1, BonusOver: true}
	if got := Points(bet, models.Match{Finished: false}); got != 0 {
		t.Errorf("unfinished match should score 0, got %d", got)
	}
	a := 2
	if got := Points(bet, models.Match{Finished: true, ScoreA: &a, ScoreB: nil}); got != 0 {
		t.Errorf("match with missing score should score 0, got %d", got)
	}
}

func TestKnockoutDrawScoresAsDraw(t *testing.T) {
	// 1-1 a.e.t. is stored as the regulation 1-1; predicting 1-1 is exact.
	// bonusOver=true is wrong here (2 goals is under 2.5), isolating the
	// exact-score points at 3.
	bet := models.Bet{PredA: 1, PredB: 1, BonusOver: true}
	if got := Points(bet, finished(1, 1)); got != 3 {
		t.Errorf("exact 1-1 should score 3, got %d", got)
	}
}
