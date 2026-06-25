package scoring

import (
	"testing"

	"bethoven/internal/models"
)

func TestWeightScheme(t *testing.T) {
	tests := []struct {
		scheme WeightScheme
		phase  models.Phase
		want   int
	}{
		// Flat: every phase ×1.
		{WeightFlat, models.PhaseGroup, 1},
		{WeightFlat, models.PhaseRound16, 1},
		{WeightFlat, models.PhaseFinal, 1},

		// Knockout: group ×1, then R32/R16 2, QF 3, SF 3, Final 4.
		{WeightKnockout, models.PhaseGroup, 1},
		{WeightKnockout, models.PhaseRound32, 2},
		{WeightKnockout, models.PhaseRound16, 2},
		{WeightKnockout, models.PhaseRound8, 3},
		{WeightKnockout, models.PhaseSemi, 3},
		{WeightKnockout, models.PhaseFinal, 4},

		// Doubling: group 1, R32/R16 2, then 4, 8, 16.
		{WeightDoubling, models.PhaseGroup, 1},
		{WeightDoubling, models.PhaseRound32, 2},
		{WeightDoubling, models.PhaseRound16, 2},
		{WeightDoubling, models.PhaseRound8, 4},
		{WeightDoubling, models.PhaseSemi, 8},
		{WeightDoubling, models.PhaseFinal, 16},

		// Linear: group 1, R32/R16 2, then 3, 4, 5.
		{WeightLinear, models.PhaseGroup, 1},
		{WeightLinear, models.PhaseRound32, 2},
		{WeightLinear, models.PhaseRound16, 2},
		{WeightLinear, models.PhaseRound8, 3},
		{WeightLinear, models.PhaseSemi, 4},
		{WeightLinear, models.PhaseFinal, 5},

		// Unknown phase ⇒ ×1 under any scheme.
		{WeightDoubling, models.Phase("nonsense"), 1},
		{WeightLinear, models.Phase(""), 1},
	}
	for _, tc := range tests {
		if got := tc.scheme.Weight(tc.phase); got != tc.want {
			t.Errorf("%s.Weight(%q) = %d, want %d", tc.scheme, tc.phase, got, tc.want)
		}
	}
}

func TestParseWeightScheme(t *testing.T) {
	tests := []struct {
		in   string
		want WeightScheme
	}{
		{"knockout", WeightKnockout},
		{"doubling", WeightDoubling},
		{"linear", WeightLinear},
		{"flat", WeightFlat},
		{"", WeightFlat},
		{"garbage", WeightFlat},
	}
	for _, tc := range tests {
		if got := ParseWeightScheme(tc.in); got != tc.want {
			t.Errorf("ParseWeightScheme(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Round-trips through String().
	for _, w := range []WeightScheme{WeightFlat, WeightKnockout, WeightDoubling, WeightLinear} {
		if got := ParseWeightScheme(w.String()); got != w {
			t.Errorf("round-trip %q -> %q", w, got)
		}
	}
}

func TestWeightLadder(t *testing.T) {
	got := WeightDoubling.Ladder()
	want := []int{1, 2, 2, 4, 8, 16}
	if len(got) != len(want) {
		t.Fatalf("Ladder len = %d, want %d", len(got), len(want))
	}
	for i, e := range got {
		if e.Mult != want[i] {
			t.Errorf("Ladder[%d] = %d (%s), want %d", i, e.Mult, e.Phase, want[i])
		}
	}
	// Ordered earliest-to-latest.
	if got[0].Phase != models.PhaseGroup || got[len(got)-1].Phase != models.PhaseFinal {
		t.Errorf("Ladder not in tournament order: %v", got)
	}
}

// ApplyWeight must keep the breakdown's Total equal to the sum of its lines, and
// equal to the weighted Score for the same inputs.
func TestApplyWeight(t *testing.T) {
	// Classic exact (3) in a final under doubling (×16) → 48.
	m := finished(2, 1)
	m.Phase = models.PhaseFinal
	bet := models.Bet{PredA: 2, PredB: 1}

	bd := Explain(ModeClassic, bet, m, Pool{}).ApplyWeight(WeightDoubling.Weight(m.Phase), m.Phase.Label())
	if bd.Total != 48 {
		t.Errorf("weighted Total = %d, want 48", bd.Total)
	}
	sum := 0
	for _, l := range bd.Lines {
		sum += l.Points
	}
	if sum != bd.Total {
		t.Errorf("sum(Lines)=%d != Total=%d (invariant broken)", sum, bd.Total)
	}
	if want := Score(ModeClassic, bet, m, Pool{}) * WeightDoubling.Weight(m.Phase); bd.Total != want {
		t.Errorf("breakdown Total=%d != weighted Score=%d", bd.Total, want)
	}

	// ×1 is a no-op (group stage / flat): no extra line, Total unchanged.
	mg := finished(2, 1)
	mg.Phase = models.PhaseGroup
	base := Explain(ModeClassic, bet, mg, Pool{})
	weighted := Explain(ModeClassic, bet, mg, Pool{}).ApplyWeight(WeightDoubling.Weight(mg.Phase), mg.Phase.Label())
	if weighted.Total != base.Total || len(weighted.Lines) != len(base.Lines) {
		t.Errorf("group-stage ×1 should be a no-op: base=%+v weighted=%+v", base, weighted)
	}

	// Zero base (wrong result) stays 0 even with a multiplier.
	wrong := models.Bet{PredA: 0, PredB: 2}
	if z := Explain(ModeClassic, wrong, m, Pool{}).ApplyWeight(16, "Final"); z.Total != 0 {
		t.Errorf("wrong-result weighted Total = %d, want 0", z.Total)
	}
}
