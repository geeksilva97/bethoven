package scoring

import "bethoven/internal/models"

// WeightScheme scales a match's points by its tournament phase, so later rounds
// can be worth more than group games. The stored setting value is the scheme's
// String(); absent/unknown ⇒ WeightFlat (every match ×1, the historical
// behaviour), so existing pools are unaffected until an admin opts in.
//
// Multipliers are whole numbers, so weighted points stay integers — nothing
// downstream (the leaderboard tiebreak, the totals maps, the TUI) has to handle
// fractions.
type WeightScheme string

const (
	WeightFlat     WeightScheme = "flat"     // every phase ×1 (default)
	WeightKnockout WeightScheme = "knockout" // ×1/2/2/3/3/4 — gentle knockout bump
	WeightDoubling WeightScheme = "doubling" // ×2 per round (steep)
	WeightLinear   WeightScheme = "linear"   // +1× per round
)

// phaseOrder lists phases earliest-to-latest; Ladder renders in this order and
// every weight table below is keyed by these phases. The Round of 32 is the first
// knockout round under the 48-team format, weighted like the Round of 16.
var phaseOrder = []models.Phase{
	models.PhaseGroup, models.PhaseRound32, models.PhaseRound16, models.PhaseRound8, models.PhaseSemi, models.PhaseFinal,
}

// weightTables holds the explicit per-phase multiplier for each non-flat scheme.
// Explicit (not a formula) so each curve is readable at a glance. The Round of 32
// is the first knockout round (48-team format) and enters at the same multiplier as
// the Round of 16. Only the group stage and unknown phases score ×1.
var weightTables = map[WeightScheme]map[models.Phase]int{
	// Gentle: knockouts matter more, but the final isn't worth a whole group stage.
	WeightKnockout: {
		models.PhaseGroup: 1, models.PhaseRound32: 2, models.PhaseRound16: 2,
		models.PhaseRound8: 3, models.PhaseSemi: 3, models.PhaseFinal: 4,
	},
	WeightDoubling: {
		models.PhaseGroup: 1, models.PhaseRound32: 2, models.PhaseRound16: 2,
		models.PhaseRound8: 4, models.PhaseSemi: 8, models.PhaseFinal: 16,
	},
	WeightLinear: {
		models.PhaseGroup: 1, models.PhaseRound32: 2, models.PhaseRound16: 2,
		models.PhaseRound8: 3, models.PhaseSemi: 4, models.PhaseFinal: 5,
	},
}

// ParseWeightScheme maps a stored setting value to a WeightScheme. Unknown/empty
// ⇒ WeightFlat.
func ParseWeightScheme(s string) WeightScheme {
	switch WeightScheme(s) {
	case WeightKnockout:
		return WeightKnockout
	case WeightDoubling:
		return WeightDoubling
	case WeightLinear:
		return WeightLinear
	default:
		return WeightFlat
	}
}

// String is the value persisted in the settings table.
func (w WeightScheme) String() string {
	switch w {
	case WeightKnockout:
		return "knockout"
	case WeightDoubling:
		return "doubling"
	case WeightLinear:
		return "linear"
	default:
		return "flat"
	}
}

// Label is the human-facing name shown in the TUI.
func (w WeightScheme) Label() string {
	switch w {
	case WeightKnockout:
		return "Knockout (2–4× later rounds)"
	case WeightDoubling:
		return "Doubling (×2 per round)"
	case WeightLinear:
		return "Linear (+1× per round)"
	default:
		return "Flat (all equal)"
	}
}

// Weight is the whole-number multiplier a match in phase p earns under the
// scheme. The group stage and any unknown phase are always ×1; WeightFlat is ×1
// for every phase.
func (w WeightScheme) Weight(p models.Phase) int {
	if tbl, ok := weightTables[w]; ok {
		if mult, ok := tbl[p]; ok {
			return mult
		}
	}
	return 1
}

// LadderEntry is one phase and its multiplier under a scheme.
type LadderEntry struct {
	Phase models.Phase
	Mult  int
}

// Ladder returns the per-phase multipliers in tournament order, so the
// scoring-rules screen can render the active scheme straight from this single
// source of truth rather than duplicating the table.
func (w WeightScheme) Ladder() []LadderEntry {
	out := make([]LadderEntry, 0, len(phaseOrder))
	for _, p := range phaseOrder {
		out = append(out, LadderEntry{Phase: p, Mult: w.Weight(p)})
	}
	return out
}
