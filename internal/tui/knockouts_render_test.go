package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"bethoven/internal/models"
	"bethoven/internal/service"
	"bethoven/internal/standings"
)

func koTestPicture() service.KnockoutPicture {
	fin := func(a, b string, sa, sb int) models.Match {
		return models.Match{TeamA: a, TeamB: b, Phase: models.PhaseRound32, Finished: true, ScoreA: &sa, ScoreB: &sb}
	}
	return service.KnockoutPicture{
		Groups: []standings.Group{{
			Label: "Group A",
			Rows: []standings.TeamRow{
				{Team: "Mexico", Played: 3, GF: 6, GA: 0, Pts: 9, Rank: 1},
				{Team: "South Korea", Played: 3, GF: 3, GA: 2, Pts: 4, Rank: 2},
				{Team: "Czechia", Played: 3, GF: 1, GA: 2, Pts: 2, Rank: 3},
				{Team: "South Africa", Played: 3, GF: 0, GA: 6, Pts: 1, Rank: 4},
			},
		}},
		ThirdPlace: []standings.ThirdPlace{
			{TeamRow: standings.TeamRow{Team: "Czechia", Played: 3, GF: 1, GA: 2, Pts: 2, Rank: 1}, Group: "Group A", Qualifies: true},
		},
		Bracket: []service.BracketRound{
			{Phase: models.PhaseRound32, Label: "Round of 32", Matches: []models.Match{
				fin("Mexico", "Brazil", 2, 1),
				{TeamA: "USA", TeamB: "Italy", Phase: models.PhaseRound32, StartsAt: time.Date(2026, 6, 29, 18, 0, 0, 0, time.UTC)},
			}},
			{Phase: models.PhaseRound16, Label: "Round of 16"},
			{Phase: models.PhaseRound8, Label: "Quarter-final"},
			{Phase: models.PhaseSemi, Label: "Semi-final"},
			{Phase: models.PhaseFinal, Label: "Final"},
		},
	}
}

func TestViewKnockoutGroups(t *testing.T) {
	m := Model{width: 100, height: 40, ko: koTestPicture(), koView: koViewGroups}
	frame := m.viewKnockoutGroups()
	for _, want := range []string{"Group A", "Mexico", "Best 3rd-placed", "Czechia"} {
		if !strings.Contains(frame, want) {
			t.Errorf("group view missing %q\n---\n%s", want, frame)
		}
	}
}

func TestViewKnockoutBracket(t *testing.T) {
	m := Model{width: 100, height: 40, ko: koTestPicture(), koView: koViewBracket}
	frame := m.viewKnockoutBracket()
	for _, want := range []string{"Round of 32", "Mexico", "USA", "Round of 16", "not drawn yet"} {
		if !strings.Contains(frame, want) {
			t.Errorf("bracket view missing %q\n---\n%s", want, frame)
		}
	}
}

// The projected bracket renders as a full R32→Final tree with team names and a
// Champion slot, drawn from the connector skeleton.
func TestViewBracketTree(t *testing.T) {
	// 16 projected ties (matches 73–88), each with recognizable team names.
	var proj []standings.ProjMatch
	for n := 73; n <= 88; n++ {
		proj = append(proj, standings.ProjMatch{
			Match:    n,
			HomeTeam: fmt.Sprintf("Home%d", n),
			AwayTeam: fmt.Sprintf("Away%d", n),
		})
	}
	pic := service.KnockoutPicture{Projected: proj} // empty Bracket ⇒ not drawn ⇒ tree shown

	m := Model{width: 120, height: 200, ko: pic, koView: koViewBracket, koTraceIdx: -1}
	frame := m.viewKnockoutBracket()
	for _, want := range []string{"ROUND OF 32", "Champion", "├", "┐", "Home74", "Away87"} {
		if !strings.Contains(frame, want) {
			t.Errorf("bracket tree missing %q", want)
		}
	}

	// Header + 63 rows for a 16-leaf bracket.
	leaves := standings.BracketLeaves(proj)
	if got := len(bracketLines(leaves, -1, nil, nil)); got != 64 {
		t.Errorf("want 64 bracket lines, got %d", got)
	}

	// Tracing a team restyles the cells on its path: team 0's leaf row (line 1)
	// changes vs the untraced render, while an off-path leaf row (team 31, the
	// last leaf at line 63) is untouched. Force a colour profile so the styling
	// is observable — tests otherwise run under lipgloss's Ascii (no-colour) profile.
	defer lipgloss.SetColorProfile(lipgloss.ColorProfile())
	lipgloss.SetColorProfile(termenv.TrueColor)
	plain := bracketLines(leaves, -1, nil, nil)
	traced := bracketLines(leaves, 0, nil, nil)
	if traced[1] == plain[1] {
		t.Errorf("traced leaf row should differ from the untraced render")
	}
	if traced[63] != plain[63] {
		t.Errorf("off-path leaf row should be unchanged when tracing another team")
	}
	// The path runs end-to-end: the Champion row (rowLevel(5,0)=31 → line 32) is
	// lit for the traced team.
	if traced[32] == plain[32] {
		t.Errorf("traced path should reach the Champion row")
	}
}

// Once R32 ties are drawn the tree must persist (not collapse to a flat list),
// with the entered teams overlaid onto their slots and a score shown when played.
func TestBracketTreePersistsWithEnteredMatches(t *testing.T) {
	var proj []standings.ProjMatch
	for n := 73; n <= 88; n++ {
		proj = append(proj, standings.ProjMatch{
			Match:    n,
			HomeTeam: fmt.Sprintf("Home%d", n),
			HomeDesc: "Winner X",
			AwayTeam: fmt.Sprintf("Away%d", n),
			AwayDesc: "Runner-up Y",
		})
	}
	sa, sb := 2, 1
	pic := service.KnockoutPicture{
		Projected: proj,
		Bracket: []service.BracketRound{{
			Phase: models.PhaseRound32, Label: "Round of 32",
			Matches: []models.Match{
				// Exact team-pair overlay onto slot 73, played 2-1.
				{TeamA: "Home73", TeamB: "Away73", Phase: models.PhaseRound32, Finished: true, ScoreA: &sa, ScoreB: &sb},
			},
		}},
	}
	m := Model{width: 120, height: 200, ko: pic, koView: koViewBracket, koTraceIdx: -1}
	frame := m.viewKnockoutBracket()
	// Tree, not the flat list.
	for _, want := range []string{"ROUND OF 32", "Champion", "Home73"} {
		if !strings.Contains(frame, want) {
			t.Errorf("entered bracket should still render the tree; missing %q\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "not drawn yet") {
		t.Errorf("tree view should not show the flat-list placeholder")
	}
}

// overlayEnteredR32 places entered ties by exact pair, then by a single
// determined non-third anchor (so a drawn winner-vs-third lands correctly even
// when the projected third differs).
func TestOverlayEnteredR32(t *testing.T) {
	proj := []standings.ProjMatch{
		{Match: 73, HomeTeam: "RunA", HomeDesc: "Runner-up A", AwayTeam: "RunB", AwayDesc: "Runner-up B"},
		{Match: 74, HomeTeam: "WinE", HomeDesc: "Winner E", AwayTeam: "GuessThird", AwayDesc: "3rd C"},
	}
	entered := []models.Match{
		{TeamA: "RunB", TeamB: "RunA"},     // exact pair (reversed) → slot 73
		{TeamA: "WinE", TeamB: "RealThird"}, // anchor on Winner E → slot 74, fixing the third
	}
	_, byNum := overlayEnteredR32(proj, entered)
	if _, ok := byNum[73]; !ok {
		t.Errorf("expected entered match on slot 73 (exact pair)")
	}
	got, ok := byNum[74]
	if !ok || got.TeamB != "RealThird" {
		t.Errorf("expected the Winner-E anchor to place the real third on slot 74, got %+v", got)
	}
}

// A team an entered tie places must not also linger in its stale projected slot.
// Regression: Sweden was projected to face Switzerland (slot 76), but the admin
// entered the official France v Sweden tie, which the anchor pass overlaid onto
// France's slot — leaving Sweden in BOTH, so it appeared twice in the tree.
func TestOverlayClearsStaleDuplicate(t *testing.T) {
	proj := []standings.ProjMatch{
		{Match: 75, HomeTeam: "France", HomeDesc: "Winner F", AwayTeam: "Croatia", AwayDesc: "Runner-up C"},
		// Sweden is projected here as a third-placed team — the uncertain slot kind
		// the anchor pass exists for, so France v Sweden anchors on France (slot 75)
		// unambiguously, leaving this stale Sweden behind.
		{Match: 76, HomeTeam: "Switzerland", HomeDesc: "Winner C", AwayTeam: "Sweden", AwayDesc: "3rd F"},
	}
	entered := []models.Match{
		{TeamA: "France", TeamB: "Sweden"}, // anchor on France → slot 75, displacing Croatia
	}
	out, byNum := overlayEnteredR32(proj, entered)
	if _, ok := byNum[75]; !ok {
		t.Fatalf("expected France v Sweden overlaid onto slot 75")
	}
	bySlot := map[int]standings.ProjMatch{}
	for _, p := range out {
		bySlot[p.Match] = p
	}
	// Slot 76 still says "Switzerland v Sweden" → Sweden would render twice. The
	// stale Sweden must be cleared back to its descriptor.
	if got := bySlot[76]; got.AwayTeam == "Sweden" {
		t.Errorf("slot 76 should no longer carry the stale Sweden, got %+v", got)
	}
	// The non-claimed side of the stale slot is untouched.
	if got := bySlot[76]; got.HomeTeam != "Switzerland" {
		t.Errorf("slot 76 home (Switzerland, unclaimed) should be preserved, got %+v", got)
	}
	// Sweden now appears exactly once across all leaf names.
	count := 0
	for _, name := range koTeamNames(out) {
		if name == "Sweden" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Sweden should appear exactly once in the bracket, got %d", count)
	}
}

// The screen opens on the bracket once a knockout match exists, else on groups.
func TestBracketDrawn(t *testing.T) {
	if !bracketDrawn(koTestPicture()) {
		t.Error("picture with R32 matches should report bracket drawn")
	}
	empty := service.KnockoutPicture{Bracket: []service.BracketRound{{Phase: models.PhaseRound32, Label: "Round of 32"}}}
	if bracketDrawn(empty) {
		t.Error("picture with no entered matches should not report bracket drawn")
	}
}
