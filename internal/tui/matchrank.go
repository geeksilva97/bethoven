package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// updateMatchRank has two modes: picking a match from the fixtures list, then
// (once rankMatch is set) showing that game's ranking — any key returns.
func (m Model) updateMatchRank(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	// Display mode: ranking already loaded; any key goes back to the menu.
	if m.rankMatch != nil {
		if key.String() == "q" {
			return m, tea.Quit
		}
		m.rankMatch, m.rankRows = nil, nil
		return m.goMenu(), nil
	}

	// Pick mode: navigate the fixtures list.
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
		mt, rows, err := m.svc.MatchLeaderboard(m.fixtures[m.fixCursor].ID)
		if err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.rankMatch, m.rankRows = mt, rows
	}
	return m, nil
}

func (m Model) viewMatchRank() string {
	if m.rankMatch == nil {
		// pick mode
		out := titleStyle.Render("Per-game ranking") + labelStyle.Render("  (pick a match)") + "\n\n"
		out += m.renderList(m.fixtures, m.fixCursor)
		out += "\n" + statusLine(m) + helpStyle.Render("↑/↓: move · enter: rank · b: back · q: quit")
		return out
	}

	// display mode
	mt := *m.rankMatch
	out := titleStyle.Render(fmt.Sprintf("%s v %s", mt.TeamA, mt.TeamB))
	out += labelStyle.Render("   result: "+fmtResult(mt)) + "\n\n"
	if !mt.Finished {
		out += lockStyle.Render("Picks are revealed once the match has a result.\n")
	} else if len(m.rankRows) == 0 {
		out += helpStyle.Render("Nobody bet on this match.\n")
	} else {
		out += labelStyle.Render(fmt.Sprintf("  %-20s %-8s %s", "player", "pick", "pts")) + "\n"
		for i, r := range m.rankRows {
			line := fmt.Sprintf("  %-20s %-8s %d", r.User.DisplayName, fmtPick(r.Bet), r.Points)
			if i == 0 {
				out += cursorOn.Render(line) + "\n"
			} else {
				out += labelStyle.Render(line) + "\n"
			}
		}
	}
	out += "\n" + helpStyle.Render("any key: back · q: quit")
	return out
}
