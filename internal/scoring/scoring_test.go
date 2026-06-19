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
		name         string
		predA, predB int
		match        models.Match
		want         int
	}{
		// exact score -> 3
		{"exact home win", 1, 0, finished(1, 0), 3},
		{"exact away win", 2, 3, finished(2, 3), 3},
		{"exact draw", 1, 1, finished(1, 1), 3},

		// correct result only (right W/D/L, wrong scoreline) -> 1
		{"home win, wrong score", 3, 0, finished(2, 1), 1},
		{"away win, wrong score", 0, 2, finished(1, 3), 1},
		{"draw, wrong score", 2, 2, finished(1, 1), 1},

		// wrong result -> 0
		{"picked away, was home win", 0, 1, finished(2, 1), 0},
		{"picked draw, was home win", 0, 0, finished(2, 0), 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bet := models.Bet{PredA: tc.predA, PredB: tc.predB}
			if got := Points(bet, tc.match); got != tc.want {
				t.Errorf("Points(%d-%d vs %d-%d) = %d, want %d",
					tc.predA, tc.predB, *tc.match.ScoreA, *tc.match.ScoreB, got, tc.want)
			}
		})
	}
}

// TestPointsEdges covers boundary cases the main matrix skips: 0-0 and high scores.
func TestPointsEdges(t *testing.T) {
	tests := []struct {
		name         string
		predA, predB int
		match        models.Match
		want         int
	}{
		{"0-0 exact", 0, 0, finished(0, 0), 3},
		{"draw result, wrong score", 2, 2, finished(0, 0), 1},
		{"high score exact", 5, 4, finished(5, 4), 3},
		{"high score result only", 9, 0, finished(4, 1), 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bet := models.Bet{PredA: tc.predA, PredB: tc.predB}
			if got := Points(bet, tc.match); got != tc.want {
				t.Errorf("Points(%d-%d vs %d-%d)=%d, want %d",
					tc.predA, tc.predB, *tc.match.ScoreA, *tc.match.ScoreB, got, tc.want)
			}
		})
	}
}

func TestPointsUnfinishedIsZero(t *testing.T) {
	bet := models.Bet{PredA: 2, PredB: 1}
	if got := Points(bet, models.Match{Finished: false}); got != 0 {
		t.Errorf("unfinished match should score 0, got %d", got)
	}
	a := 2
	if got := Points(bet, models.Match{Finished: true, ScoreA: &a, ScoreB: nil}); got != 0 {
		t.Errorf("match with missing away score should score 0, got %d", got)
	}
	if got := Points(bet, models.Match{Finished: true, ScoreA: nil, ScoreB: &a}); got != 0 {
		t.Errorf("match with missing home score should score 0, got %d", got)
	}
}

func TestKnockoutDrawScoresAsDraw(t *testing.T) {
	// 1-1 a.e.t. is stored as the regulation 1-1; predicting 1-1 is exact -> 3.
	bet := models.Bet{PredA: 1, PredB: 1}
	if got := Points(bet, finished(1, 1)); got != 3 {
		t.Errorf("exact 1-1 should score 3, got %d", got)
	}
}

// TestIsExactAndIsCorrectResult covers the mode-agnostic tiebreaker helpers the
// leaderboard uses: exact scoreline, correct result (a superset of exact), wrong
// result, and unscored/unfinished matches (both false).
func TestIsExactAndIsCorrectResult(t *testing.T) {
	tests := []struct {
		name              string
		predA, predB      int
		match             models.Match
		wantExact, wantOK bool
	}{
		{"exact home win", 2, 1, finished(2, 1), true, true},
		{"exact draw", 0, 0, finished(0, 0), true, true},
		{"result only, wrong score", 3, 0, finished(2, 1), false, true},
		{"draw result, wrong score", 2, 2, finished(1, 1), false, true},
		{"wrong result", 0, 1, finished(2, 1), false, false},
		{"unfinished match", 2, 1, models.Match{Finished: false}, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := models.Bet{PredA: tc.predA, PredB: tc.predB}
			if got := IsExact(b, tc.match); got != tc.wantExact {
				t.Errorf("IsExact = %v, want %v", got, tc.wantExact)
			}
			if got := IsCorrectResult(b, tc.match); got != tc.wantOK {
				t.Errorf("IsCorrectResult = %v, want %v", got, tc.wantOK)
			}
		})
	}
}
