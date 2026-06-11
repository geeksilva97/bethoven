// Package scoring implements BEThoven's point rules as a pure function. It has
// no I/O and no DB dependency, so it is exhaustively unit-tested.
//
// Per match (max 3 points):
//   - exact score:        3   (already implies the correct result)
//   - correct result only: 1   (right W/D/L, wrong scoreline)
//
// Knockout matches store the regulation 90-minute score, so the same rules
// apply unchanged — a 1-1 that went to penalties scores as a 1-1 draw.
package scoring

import "bethoven/internal/models"

// Points returns the points a bet earns against a match. It returns 0 until the
// match has a recorded result.
func Points(b models.Bet, m models.Match) int {
	if !m.Finished || m.ScoreA == nil || m.ScoreB == nil {
		return 0
	}
	sa, sb := *m.ScoreA, *m.ScoreB

	switch {
	case b.PredA == sa && b.PredB == sb:
		return 3 // exact score
	case sign(b.PredA-b.PredB) == sign(sa-sb):
		return 1 // correct result only
	}
	return 0
}

// sign returns -1, 0, or +1, collapsing a goal difference into a W/D/L outcome.
func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	default:
		return 0
	}
}
