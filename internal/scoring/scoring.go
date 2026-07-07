// Package scoring implements BEThoven's point rules as pure functions. It has
// no I/O and no DB dependency, so it is exhaustively unit-tested.
//
// Three selectable modes (the admin picks one; see service.ScoringMode):
//
//   - Classic (default): exact score 3, correct result (W/D/L) 1, miss 0. The
//     tiers are mutually exclusive — an exact score is 3, not 3+1.
//   - Proximity: the closer the scoreline, the more points. 0 for a wrong
//     result; otherwise max(1, 5 − distance), where distance is the total goals
//     off (|predA−scoreA| + |predB−scoreB|). Exact (distance 0) → 5; one goal
//     off → 4; … with a floor of 1 for at least calling the winner. This is the
//     "deduction per goal" model used by pools like Kicktipp.
//   - Scarcity: Proximity, plus a contrarian bonus for picks few others made —
//     +2 when a correct result was picked by <25% of the match's bets, and +2
//     when a correct exact score was picked by <10%. The bonus only applies once
//     a match has at least ScarcityQuorum bets, so small fields don't reward
//     statistical noise; below that it scores as plain Proximity. Scarcity is
//     pool-relative, so it takes a Pool the caller (the service) computes.
//
// Knockout matches store the score at the final whistle — regulation plus extra
// time if it was played, but NOT penalties — so the same rules apply unchanged: a
// 1-1 after extra time that is decided on penalties scores as a 1-1 draw.
package scoring

import "bethoven/internal/models"

// Mode selects which point rule applies. The zero value is ModeClassic, so a
// missing/unknown setting yields the historical behaviour.
type Mode int

const (
	ModeClassic   Mode = iota // exact 3 / result 1 / miss 0
	ModeProximity             // max(1, 5−distance) for a correct result, else 0
	ModeScarcity              // Proximity + contrarian bonus (needs Pool)
)

// Tunable constants for Proximity/Scarcity. Kept named so they are easy to find
// and adjust.
const (
	proximityMax   = 5 // points for an exact score
	proximityFloor = 1 // points for a correct result, however far the scoreline

	scarcityResultBonus = 2 // for a correct result few others picked
	scarcityExactBonus  = 2 // for a correct exact score even fewer picked
	// Thresholds expressed as integer fractions (numerator/denominator) to avoid
	// floating point: a pick qualifies when count*denom < total*num.
	scarcityResultNum, scarcityResultDenom = 1, 4  // < 25% of the match's bets
	scarcityExactNum, scarcityExactDenom   = 1, 10 // < 10% of the match's bets
)

// ScarcityQuorum is the minimum number of bets on a match before any
// contrarian bonus applies. Below it a percentage is just noise — being the
// lone "rare" picker in a 4-person field is luck, not contrarianism — and
// the +2/+4 swing it would create unfairly buries everyone else. Below
// quorum, Scarcity scores identically to plain Proximity. Exported so the
// achievements Contrarian badge gates on the same quorum.
const ScarcityQuorum = 8

// Pool is the per-match pick distribution for the bet being scored. Counts
// include the bet itself. Only ModeScarcity reads it; other modes ignore it, so
// callers may pass the zero value.
type Pool struct {
	Total      int // total bets placed on the match
	SameResult int // bets sharing this bet's W/D/L result
	SameExact  int // bets sharing this bet's exact scoreline
}

// ParseMode maps a stored setting value to a Mode. Unknown/empty → ModeClassic.
func ParseMode(s string) Mode {
	switch s {
	case "proximity":
		return ModeProximity
	case "scarcity":
		return ModeScarcity
	default:
		return ModeClassic
	}
}

// String is the value persisted in the settings table.
func (m Mode) String() string {
	switch m {
	case ModeProximity:
		return "proximity"
	case ModeScarcity:
		return "scarcity"
	default:
		return "classic"
	}
}

// Label is the human-facing name shown in the TUI.
func (m Mode) Label() string {
	switch m {
	case ModeProximity:
		return "Proximity"
	case ModeScarcity:
		return "Scarcity"
	default:
		return "Classic"
	}
}

// Result collapses a bet into its W/D/L sign (-1/0/+1), matching how the scoring
// functions classify outcomes. The service uses it to bucket the pick pool.
func Result(b models.Bet) int { return sign(b.PredA - b.PredB) }

// IsExact reports whether the bet's scoreline exactly matches the match result.
// Mode-agnostic: an exact score is exact under Classic, Proximity, and Scarcity
// alike. Used by the service as the leaderboard's first tiebreaker. False until
// the match has a recorded result.
func IsExact(b models.Bet, m models.Match) bool {
	return scored(m) && b.PredA == *m.ScoreA && b.PredB == *m.ScoreB
}

// IsCorrectResult reports whether the bet called the W/D/L outcome (an exact score
// counts — it is a superset of IsExact). The leaderboard's second tiebreaker.
// False until the match has a recorded result.
func IsCorrectResult(b models.Bet, m models.Match) bool {
	return scored(m) && sign(b.PredA-b.PredB) == sign(*m.ScoreA-*m.ScoreB)
}

// Score dispatches to the active mode. Classic and Proximity ignore pool, so the
// caller may pass the zero Pool for them.
func Score(mode Mode, b models.Bet, m models.Match, pool Pool) int {
	switch mode {
	case ModeProximity:
		return ProximityPoints(b, m)
	case ModeScarcity:
		return ScarcityPoints(b, m, pool)
	default:
		return Points(b, m)
	}
}

// Points returns the Classic points a bet earns against a match. It returns 0
// until the match has a recorded result.
func Points(b models.Bet, m models.Match) int {
	if !scored(m) {
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

// ProximityPoints returns the Proximity points: 0 for a wrong result, otherwise
// max(floor, proximityMax − distance). An exact score has distance 0 → max.
func ProximityPoints(b models.Bet, m models.Match) int {
	if !scored(m) {
		return 0
	}
	sa, sb := *m.ScoreA, *m.ScoreB
	if sign(b.PredA-b.PredB) != sign(sa-sb) {
		return 0 // wrong result
	}
	d := abs(b.PredA-sa) + abs(b.PredB-sb)
	return max(proximityFloor, proximityMax-d)
}

// ScarcityPoints returns the Proximity points plus a contrarian bonus for rare
// correct picks. No bonus is added for a wrong result (base is already 0) or
// when the match has fewer than ScarcityQuorum bets (too few for "rare" to
// mean anything) — in both cases it returns the Proximity base.
func ScarcityPoints(b models.Bet, m models.Match, pool Pool) int {
	base := ProximityPoints(b, m)
	if base == 0 {
		return 0
	}
	// Too few bettors on the match for "rare" to be meaningful: no bonus, so
	// Scarcity falls back to plain Proximity.
	if pool.Total < ScarcityQuorum {
		return base
	}
	bonus := 0
	// Correct result that few others picked.
	if rare(pool.SameResult, pool.Total, scarcityResultNum, scarcityResultDenom) {
		bonus += scarcityResultBonus
	}
	// Correct exact score that even fewer picked.
	if b.PredA == *m.ScoreA && b.PredB == *m.ScoreB &&
		rare(pool.SameExact, pool.Total, scarcityExactNum, scarcityExactDenom) {
		bonus += scarcityExactBonus
	}
	return base + bonus
}

// rare reports whether count/total < num/denom (strictly), guarding total>0.
func rare(count, total, num, denom int) bool {
	return total > 0 && count*denom < total*num
}

// scored reports whether a match has a usable recorded result.
func scored(m models.Match) bool {
	return m.Finished && m.ScoreA != nil && m.ScoreB != nil
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

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
