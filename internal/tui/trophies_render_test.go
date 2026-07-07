package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/achievements"
)

// trophyModel builds a model on the trophies screen with a hand-made board:
// one held badge, everything else unclaimed.
func trophyModel(t *testing.T) Model {
	t.Helper()
	m := adminModel(t)
	m.screen = screenTrophies
	m.trophies = achievements.Compute(achievements.Input{Players: []achievements.PlayerInput{
		{UserID: 2, Name: "Alice", Picks: []achievements.Pick{
			{Round: "r1", PredA: 2, PredB: 1, ScoreA: 2, ScoreB: 1, Points: 3, Exact: true, Correct: true, ResultShare: -1},
			{Round: "r2", PredA: 0, PredB: 0, ScoreA: 0, ScoreB: 0, Points: 3, Exact: true, Correct: true, ResultShare: -1},
		}},
	}})
	return m
}

// TestTrophiesRender: the board shows the whole catalog, the holder with their
// detail line, and unclaimed badges as targets.
func TestTrophiesRender(t *testing.T) {
	m := trophyModel(t)
	m.height = 80 // tall enough that no badge is clipped by the window

	out := m.viewTrophies()
	if !strings.Contains(out, "The Oracle") {
		t.Fatalf("missing badge name:\n%s", out)
	}
	if !strings.Contains(out, "Alice") || !strings.Contains(out, "2 exact scores") {
		t.Fatalf("missing holder + detail:\n%s", out)
	}
	if !strings.Contains(out, "unclaimed") {
		t.Fatalf("unclaimed badges should render as targets:\n%s", out)
	}
}

// TestTrophiesMenuEntry: the Achievements menu item exists for everyone and
// enter loads the board (empty pool ⇒ all-unclaimed, not an error).
func TestTrophiesMenuEntry(t *testing.T) {
	m := adminModel(t)
	m.buildMenu()
	idx := -1
	for i, it := range m.menuItems {
		if it.target == screenTrophies {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("no Achievements entry in the menu")
	}
	m.menuCursor = idx
	next, _ := m.updateMenu(keyMsg("enter"))
	m = next.(Model)
	if m.screen != screenTrophies {
		t.Fatalf("enter on Achievements → screen %d, want screenTrophies", m.screen)
	}
	if len(m.trophies.Standings) != len(achievements.Catalog) {
		t.Fatalf("board rows = %d, want the whole catalog (%d)", len(m.trophies.Standings), len(achievements.Catalog))
	}
}

// TestTrophiesCursorMoves: j/k move the selection and esc returns to the menu.
func TestTrophiesCursorMoves(t *testing.T) {
	m := trophyModel(t)

	next, _ := m.updateTrophies(keyMsg("j"))
	m = next.(Model)
	if m.trophyCursor != 1 {
		t.Fatalf("cursor after j = %d, want 1", m.trophyCursor)
	}
	next, _ = m.updateTrophies(keyMsg("k"))
	m = next.(Model)
	if m.trophyCursor != 0 {
		t.Fatalf("cursor after k = %d, want 0", m.trophyCursor)
	}

	next, _ = m.updateTrophies(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.screen != screenMenu {
		t.Fatalf("esc should return to the menu, on screen %d", m.screen)
	}
}
