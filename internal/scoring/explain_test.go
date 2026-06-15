package scoring

import (
	"strings"
	"testing"

	"bethoven/internal/models"
)

// TestExplainTotalMatchesScore is the headline invariant: a breakdown's Total
// must equal Score for the same inputs, across every mode and a range of pools.
func TestExplainTotalMatchesScore(t *testing.T) {
	bets := []models.Bet{
		{PredA: 4, PredB: 1}, // exact
		{PredA: 3, PredB: 1}, // correct result, 1 off
		{PredA: 1, PredB: 0}, // correct result, far
		{PredA: 0, PredB: 1}, // wrong result
		{PredA: 2, PredB: 2}, // draw vs a home win (wrong)
	}
	m := finished(4, 1)
	pools := []Pool{
		{},
		{Total: 7, SameResult: 1, SameExact: 1},  // below quorum
		{Total: 8, SameResult: 1, SameExact: 1},  // at quorum, rare result
		{Total: 20, SameResult: 1, SameExact: 1}, // rare result + rare exact
		{Total: 20, SameResult: 9, SameExact: 5}, // popular
	}
	for _, mode := range []Mode{ModeClassic, ModeProximity, ModeScarcity} {
		for _, b := range bets {
			for _, p := range pools {
				bd := Explain(mode, b, m, p)
				if got := Score(mode, b, m, p); bd.Total != got {
					t.Errorf("mode=%v bet=%d-%d pool=%+v: breakdown Total=%d, Score=%d",
						mode, b.PredA, b.PredB, p, bd.Total, got)
				}
			}
		}
	}
}

func TestExplainScarcityNotes(t *testing.T) {
	exact := models.Bet{PredA: 4, PredB: 1}
	m := finished(4, 1)

	// Below quorum: a single line explaining the gate, no bonus points.
	bd := Explain(ModeScarcity, exact, m, Pool{Total: 5, SameResult: 1, SameExact: 1})
	if bd.Total != 5 {
		t.Fatalf("below-quorum total = %d, want 5", bd.Total)
	}
	if !hasNoteContaining(bd, "needs 8+ bets") {
		t.Errorf("below-quorum breakdown missing quorum note: %+v", bd.Lines)
	}

	// Rare result + rare exact: both bonuses fire (1 of 20 = 5%).
	bd = Explain(ModeScarcity, exact, m, Pool{Total: 20, SameResult: 1, SameExact: 1})
	if bd.Total != 9 {
		t.Fatalf("rare total = %d, want 9", bd.Total)
	}
	if !hasLine(bd, "Rare-result bonus", 2) || !hasLine(bd, "Rare-exact bonus", 2) {
		t.Errorf("rare breakdown missing a +2 bonus line: %+v", bd.Lines)
	}

	// Popular result: bonus line present but worth 0, with a "needs <25%" note.
	bd = Explain(ModeScarcity, exact, m, Pool{Total: 20, SameResult: 10, SameExact: 1})
	if !hasLine(bd, "Rare-result bonus", 0) || !hasNoteContaining(bd, "needs <25%") {
		t.Errorf("popular-result breakdown should show a 0 bonus with a needs-note: %+v", bd.Lines)
	}
}

func hasLine(bd Breakdown, label string, points int) bool {
	for _, l := range bd.Lines {
		if l.Label == label && l.Points == points {
			return true
		}
	}
	return false
}

func hasNoteContaining(bd Breakdown, sub string) bool {
	for _, l := range bd.Lines {
		if strings.Contains(l.Note, sub) {
			return true
		}
	}
	return false
}
