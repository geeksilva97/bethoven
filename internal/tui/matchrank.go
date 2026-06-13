package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/models"
)

// updateMatchRank has two modes: picking a match from the fixtures list, then
// (once rankMatch is set) showing that game's ranking — any key returns.
func (m Model) updateMatchRank(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	// Display mode: ranking already loaded; any key goes back to the match
	// list so you can pick another game without returning to the menu.
	if m.rankMatch != nil {
		if key.String() == "q" {
			return m, tea.Quit
		}
		m.rankMatch, m.rankRows = nil, nil
		return m, nil
	}

	// Pick mode: navigate the (optionally filtered) fixtures list.
	vis := filterMatches(m.fixtures, m.rankSearch.query())

	if m.rankSearch.active {
		switch key.String() {
		case "esc":
			m.rankSearch.close()
			m.fixCursor = 0
			return m, nil
		case "enter":
			return m.openRank(vis)
		case "up":
			if m.fixCursor > 0 {
				m.fixCursor--
			}
			return m, nil
		case "down":
			if m.fixCursor < len(vis)-1 {
				m.fixCursor++
			}
			return m, nil
		}
		cmd := m.rankSearch.update(msg)
		if n := len(filterMatches(m.fixtures, m.rankSearch.query())); m.fixCursor >= n {
			m.fixCursor = n - 1
		}
		if m.fixCursor < 0 {
			m.fixCursor = 0
		}
		return m, cmd
	}

	switch key.String() {
	case "q":
		return m, tea.Quit
	case "esc", "b":
		return m.goMenu(), nil
	case "/":
		return m, m.rankSearch.open()
	case "up", "k":
		if m.fixCursor > 0 {
			m.fixCursor--
		}
	case "down", "j":
		if m.fixCursor < len(vis)-1 {
			m.fixCursor++
		}
	case "enter":
		return m.openRank(vis)
	}
	return m, nil
}

// openRank loads and shows the per-match ranking for the highlighted match.
func (m Model) openRank(vis []models.Match) (tea.Model, tea.Cmd) {
	if len(vis) == 0 {
		return m, nil
	}
	mt, rows, err := m.svc.MatchLeaderboard(vis[m.fixCursor].ID)
	if err != nil {
		m.setStatus(err.Error(), true)
		return m, nil
	}
	m.rankMatch, m.rankRows = mt, rows
	return m, nil
}

func (m Model) viewMatchRank() string {
	if m.rankMatch == nil {
		// pick mode
		out := titleStyle.Render("Per-game ranking") + labelStyle.Render("  (pick a match)") + "\n\n"
		out += m.rankSearch.view()
		vis := filterMatches(m.fixtures, m.rankSearch.query())
		if len(vis) == 0 {
			out += helpStyle.Render("  no matches.\n")
		} else {
			out += m.renderList(vis, m.fixCursor)
		}
		help := "↑/↓: move · enter: rank · /: search · b: back · q: quit"
		if m.rankSearch.active {
			help = "type to filter · ↑/↓: move · enter: rank · esc: clear"
		}
		out += "\n" + statusLine(m) + helpStyle.Render(help)
		return out
	}

	// display mode
	mt := *m.rankMatch
	out := titleStyle.Render(fmt.Sprintf("%s v %s", withFlag(mt.TeamA), withFlag(mt.TeamB)))
	if mt.Live {
		out += "   " + liveScore(mt) + "\n\n"
	} else {
		out += labelStyle.Render("   result: "+fmtResult(mt)) + "\n\n"
	}
	if !mt.Finished {
		note := "Picks are revealed once the match has a result.\n"
		if mt.Live {
			note = liveLegend + "\n" + "Picks are revealed once the match has a result.\n"
		}
		out += lockStyle.Render(note)
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
	out += "\n" + helpStyle.Render("any key: back to games · q: quit")
	return out
}
