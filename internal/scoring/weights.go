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
	WeightDoubling WeightScheme = "doubling" // ×1/2/4/8/16 by round
	WeightLinear   WeightScheme = "linear"   // ×1/2/3/4/5 by round
)

// phaseOrder lists phases earliest-to-latest. The multipliers below are keyed by
// a phase's index here (group = 0 … final = 4); Ladder renders in this order.
var phaseOrder = []models.Phase{
	models.PhaseGroup, models.PhaseRound16, models.PhaseRound8, models.PhaseSemi, models.PhaseFinal,
}

// ParseWeightScheme maps a stored setting value to a WeightScheme. Unknown/empty
// ⇒ WeightFlat.
func ParseWeightScheme(s string) WeightScheme {
	switch WeightScheme(s) {
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
	if w == WeightFlat {
		return 1
	}
	idx := phaseIndex(p) // 0 group … 4 final, -1 unknown
	if idx <= 0 {
		return 1
	}
	switch w {
	case WeightDoubling:
		return 1 << idx // 1, 2, 4, 8, 16
	case WeightLinear:
		return idx + 1 // 1, 2, 3, 4, 5
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

func phaseIndex(p models.Phase) int {
	for i, q := range phaseOrder {
		if q == p {
			return i
		}
	}
	return -1
}
