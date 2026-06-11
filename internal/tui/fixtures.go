package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/models"
)

// matchLine renders one fixture row for a list, marking lock state.
func (m Model) matchLine(mt models.Match, selected bool) string {
	locked := !m.svc.Now().Before(mt.StartsAt.UTC())
	when := mt.StartsAt.UTC().Format("Mon 02 Jan 15:04")
	label := fmt.Sprintf("%-14s v %-14s  %s", mt.TeamA, mt.TeamB, when)
	if mt.GroupLabel != "" {
		label += "  " + mt.GroupLabel
	}

	tag := ""
	switch {
	case mt.Finished && mt.ScoreA != nil:
		tag = okStyle.Render(fmt.Sprintf("  [%d-%d]", *mt.ScoreA, *mt.ScoreB))
	case locked:
		tag = lockStyle.Render("  🔒 locked")
	}

	cursor := "  "
	if selected {
		cursor = cursorOn.Render("▸ ")
		label = cursorOn.Render(label)
	} else {
		label = labelStyle.Render(label)
	}
	return cursor + label + tag
}

func (m Model) updateFixtures(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "q":
		return m, tea.Quit
	case "esc", "b":
		return m.goMenu(), nil
	case "up", "k":
		if m.fixCursor > 0 {
			m.fixCursor--
		}
	case "down", "j":
		if m.fixCursor < len(m.fixtures)-1 {
			m.fixCursor++
		}
	case "enter":
		if len(m.fixtures) == 0 {
			return m, nil
		}
		mt := m.fixtures[m.fixCursor]
		if mt.Finished || !m.svc.Now().Before(mt.StartsAt.UTC()) {
			m.setStatus("that match is closed — betting ended at kickoff", true)
			return m, nil
		}
		return m.openBet(mt), textinput.Blink
	}
	return m, nil
}

func (m Model) viewFixtures() string {
	out := titleStyle.Render("Fixtures") + labelStyle.Render("  (pick a match to bet)") + "\n\n"
	if len(m.fixtures) == 0 {
		out += helpStyle.Render("No matches yet.\n")
	}
	for i, mt := range m.fixtures {
		out += m.matchLine(mt, i == m.fixCursor) + "\n"
	}
	out += "\n" + statusLine(m) + helpStyle.Render("↑/↓: move · enter: bet · b: back · q: quit")
	return out
}
