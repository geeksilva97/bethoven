package scoring

import (
	"fmt"

	"bethoven/internal/models"
)

// BreakdownLine is one row of a points explanation: a label, the points it
// contributes to the total (may be 0), and an optional note explaining why —
// e.g. how far off the scoreline was, or what pool share a bonus needed.
type BreakdownLine struct {
	Label  string
	Points int
	Note   string
}

// Breakdown explains how a bet scored under a mode: the contributing lines and
// their Total. Invariant: Total equals Score(mode, bet, match, pool) for the
// same inputs — the explanation and the scorer never disagree.
type Breakdown struct {
	Mode  Mode
	Lines []BreakdownLine
	Total int
}

// Explain produces a human-readable breakdown of a bet's points under the
// active mode. It mirrors Score line-for-line, so the per-game ranking can show
// exactly why a player earned what they did (and, for Scarcity, why a contrarian
// bonus did or did not fire). Pool is only consulted for Scarcity.
func Explain(mode Mode, b models.Bet, m models.Match, pool Pool) Breakdown {
	bd := Breakdown{Mode: mode}
	if !scored(m) {
		bd.add("No result yet", 0, "")
		return bd
	}
	switch mode {
	case ModeProximity:
		bd.explainProximity(b, m)
	case ModeScarcity:
		bd.explainScarcity(b, m, pool)
	default:
		bd.explainClassic(b, m)
	}
	for _, l := range bd.Lines {
		bd.Total += l.Points
	}
	return bd
}

func (bd *Breakdown) add(label string, points int, note string) {
	bd.Lines = append(bd.Lines, BreakdownLine{Label: label, Points: points, Note: note})
}

// ApplyWeight scales a breakdown by a whole-number round multiplier (see
// WeightScheme). When the multiplier is >1 and the base scored anything, it
// appends a line carrying the added points and bumps Total, so the invariant
// Total == sum(Lines) holds and the explanation still matches the weighted
// Score. A ×1 multiplier or a zero base is a no-op.
func (bd Breakdown) ApplyWeight(weight int, phaseLabel string) Breakdown {
	if weight <= 1 || bd.Total == 0 {
		return bd
	}
	added := bd.Total*weight - bd.Total
	bd.add(fmt.Sprintf("%s ×%d", phaseLabel, weight), added, "knockout round multiplier")
	bd.Total *= weight
	return bd
}

func (bd *Breakdown) explainClassic(b models.Bet, m models.Match) {
	sa, sb := *m.ScoreA, *m.ScoreB
	switch {
	case b.PredA == sa && b.PredB == sb:
		bd.add("Exact score", 3, "")
	case sign(b.PredA-b.PredB) == sign(sa-sb):
		bd.add("Correct result (W/D/L)", 1, "wrong scoreline")
	default:
		bd.add("Wrong result", 0, "")
	}
}

// proximityLine appends the base line shared by Proximity and Scarcity and
// returns the base points (0 for a wrong result).
func (bd *Breakdown) proximityLine(b models.Bet, m models.Match) int {
	sa, sb := *m.ScoreA, *m.ScoreB
	if sign(b.PredA-b.PredB) != sign(sa-sb) {
		bd.add("Wrong result", 0, "")
		return 0
	}
	d := abs(b.PredA-sa) + abs(b.PredB-sb)
	pts := max(proximityFloor, proximityMax-d)
	switch {
	case d == 0:
		bd.add("Exact score", pts, "")
	case proximityMax-d >= proximityFloor:
		bd.add("Correct result", pts, fmt.Sprintf("%s off", goals(d)))
	default:
		bd.add("Correct result", pts, fmt.Sprintf("%s off — floored to %d", goals(d), proximityFloor))
	}
	return pts
}

func (bd *Breakdown) explainProximity(b models.Bet, m models.Match) {
	bd.proximityLine(b, m)
}

func (bd *Breakdown) explainScarcity(b models.Bet, m models.Match, pool Pool) {
	base := bd.proximityLine(b, m)
	if base == 0 {
		return // wrong result: no bonus possible
	}
	if pool.Total < scarcityQuorum {
		bd.add("Contrarian bonus", 0,
			fmt.Sprintf("needs %d+ bets on the match, only %d", scarcityQuorum, pool.Total))
		return
	}

	// Result bonus: correct result few others picked.
	resPct := percent(pool.SameResult, pool.Total)
	resThresh := percent(scarcityResultNum, scarcityResultDenom)
	if rare(pool.SameResult, pool.Total, scarcityResultNum, scarcityResultDenom) {
		bd.add("Rare-result bonus", scarcityResultBonus,
			fmt.Sprintf("%d of %d picked this result (%d%%) — under %d%%",
				pool.SameResult, pool.Total, resPct, resThresh))
	} else {
		bd.add("Rare-result bonus", 0,
			fmt.Sprintf("%d of %d picked this result (%d%%) — needs <%d%%",
				pool.SameResult, pool.Total, resPct, resThresh))
	}

	// Exact bonus: only relevant when the pick nailed the scoreline.
	if b.PredA == *m.ScoreA && b.PredB == *m.ScoreB {
		exPct := percent(pool.SameExact, pool.Total)
		exThresh := percent(scarcityExactNum, scarcityExactDenom)
		if rare(pool.SameExact, pool.Total, scarcityExactNum, scarcityExactDenom) {
			bd.add("Rare-exact bonus", scarcityExactBonus,
				fmt.Sprintf("%d of %d nailed the score (%d%%) — under %d%%",
					pool.SameExact, pool.Total, exPct, exThresh))
		} else {
			bd.add("Rare-exact bonus", 0,
				fmt.Sprintf("%d of %d nailed the score (%d%%) — needs <%d%%",
					pool.SameExact, pool.Total, exPct, exThresh))
		}
	}
}

// percent is integer n/total as a rounded-down percentage; 0 when total is 0.
func percent(n, total int) int {
	if total == 0 {
		return 0
	}
	return n * 100 / total
}

// goals renders a goal-distance with correct pluralisation ("1 goal" / "2 goals").
func goals(d int) string {
	if d == 1 {
		return "1 goal"
	}
	return fmt.Sprintf("%d goals", d)
}
