package tui

import (
	"strings"
	"testing"
)

func TestCardOrdinalAndPlace(t *testing.T) {
	cases := map[int]string{1: "1st", 2: "2nd", 3: "3rd", 4: "4th", 11: "11th", 12: "12th", 21: "21st", 23: "23rd"}
	for n, want := range cases {
		if got := cardOrdinal(n); got != want {
			t.Errorf("cardOrdinal(%d) = %q, want %q", n, got, want)
		}
	}
	if cardPlace(1) != "Champion" || cardPlace(2) != "Runner-up" || cardPlace(3) != "Third place" {
		t.Error("top-three place labels wrong")
	}
	if got := cardPlace(7); got != "7th place" {
		t.Errorf("cardPlace(7) = %q, want \"7th place\"", got)
	}
}

func TestMedalEmoji(t *testing.T) {
	if medalEmoji(1) != "🥇" || medalEmoji(2) != "🥈" || medalEmoji(3) != "🥉" {
		t.Error("podium medals wrong")
	}
	if medalEmoji(4) != "" {
		t.Error("non-podium should have no medal")
	}
}

// sparkline scales values so the largest is the tallest bar; the leader (highest
// passed value) peaks and the worst sits lowest.
func TestSparkline(t *testing.T) {
	if got := sparkline(nil); got != "" {
		t.Errorf("empty sparkline = %q", got)
	}
	// Pass negated positions: champion (pos 1 → -1) is the max ⇒ tallest bar.
	s := sparkline([]int{-3, -2, -1})
	r := []rune(s)
	if len(r) != 3 {
		t.Fatalf("expected 3 bars, got %d (%q)", len(r), s)
	}
	if r[2] != '█' {
		t.Errorf("leader bar should be the tallest █, got %q", string(r[2]))
	}
	if r[0] != '▁' {
		t.Errorf("worst bar should be the shortest ▁, got %q", string(r[0]))
	}
	// Equal values render at a single mid height (no divide-by-zero).
	if flat := sparkline([]int{5, 5, 5}); strings.Count(flat, string(flat[0:len(flat)/3])) == 0 {
		t.Errorf("flat sparkline unexpectedly empty: %q", flat)
	}
}

func TestSplitParagraphs(t *testing.T) {
	// Short text (≤ perPara sentences) stays one block, no blank lines.
	if got := splitParagraphs("You won. Nicely done.", 2); strings.Contains(got, "\n") {
		t.Errorf("short text should not break: %q", got)
	}
	// Four sentences at 2/para ⇒ two paragraphs separated by a blank line.
	in := "You started cold. You climbed fast. Then you stalled. But you held on."
	got := splitParagraphs(in, 2)
	if parts := strings.Split(got, "\n\n"); len(parts) != 2 {
		t.Fatalf("expected 2 paragraphs, got %d: %q", len(parts), got)
	}
	// Mid-sentence tokens with punctuation must NOT trigger a break.
	noBreak := "You called it 5-0 and the odds were 2.5 to one all night long no break here."
	if strings.Contains(splitParagraphs(noBreak, 1), "\n") {
		t.Errorf("scores/decimals must not split a sentence: %q", splitParagraphs(noBreak, 1))
	}
	// A terminator followed by a lowercase word (e.g. an abbreviation) is not a break.
	if strings.Contains(splitParagraphs("seeded no. one for the title run here.", 1), "\n") {
		t.Error("abbreviation 'no.' followed by lowercase should not split")
	}
}

func TestCardBorderColor(t *testing.T) {
	if cardBorderColor(1) != gold || cardBorderColor(2) != silver || cardBorderColor(3) != bronze {
		t.Error("podium border colors wrong")
	}
	if cardBorderColor(0) != dim {
		t.Error("non-podium border should be dim")
	}
}
