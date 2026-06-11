package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/models"
)

type menuItem struct {
	label  string
	target screen
}

// buildMenu assembles the main menu, adding admin entries for admins.
func (m *Model) buildMenu() {
	items := []menuItem{
		{"Place / edit bets", screenFixtures},
		{"My results", screenMyResults},
		{"Leaderboard", screenLeaderboard},
		{"Per-game ranking", screenMatchRank},
	}
	if m.user != nil && m.user.Role == models.RoleAdmin {
		items = append(items,
			menuItem{"⚙  Admin: add match", screenAddMatch},
			menuItem{"⚙  Admin: enter result", screenEnterResult},
			menuItem{"⚙  Admin: all bets", screenAllBets},
		)
	}
	m.menuItems = items
	if m.menuCursor >= len(items) {
		m.menuCursor = 0
	}
}

func (m Model) updateMenu(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "q", "esc":
		return m, tea.Quit
	case "up", "k":
		if m.menuCursor > 0 {
			m.menuCursor--
		}
	case "down", "j":
		if m.menuCursor < len(m.menuItems)-1 {
			m.menuCursor++
		}
	case "enter":
		return m.enterMenuItem(m.menuItems[m.menuCursor].target)
	}
	return m, nil
}

// enterMenuItem loads the data for the chosen screen and switches to it.
func (m Model) enterMenuItem(target screen) (tea.Model, tea.Cmd) {
	m.status = ""
	switch target {
	case screenFixtures:
		fx, err := m.svc.Fixtures()
		if err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.fixtures, m.fixCursor, m.screen = fx, 0, screenFixtures
	case screenMyResults:
		rows, total, err := m.svc.MyResults(m.user.ID)
		if err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.myRows, m.myTotal, m.screen = rows, total, screenMyResults
	case screenLeaderboard:
		board, err := m.svc.Leaderboard()
		if err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.standings, m.screen = board, screenLeaderboard
	case screenMatchRank:
		// Reuse the fixtures list to pick which game to rank.
		fx, err := m.svc.Fixtures()
		if err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.fixtures, m.fixCursor = fx, 0
		m.rankRows, m.rankMatch = nil, nil
		m.screen = screenMatchRank
	case screenAddMatch:
		m.initAddMatch()
		m.screen = screenAddMatch
	case screenEnterResult:
		fx, err := m.svc.Fixtures()
		if err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.fixtures, m.resCursor, m.resMatch = fx, 0, nil
		m.screen = screenEnterResult
	case screenAllBets:
		grid, err := m.svc.AllBets(m.user)
		if err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.grid, m.screen = grid, screenAllBets
	}
	return m, nil
}

func (m Model) viewMenu() string {
	out := titleStyle.Render("🎼  BEThoven") + "  "
	if m.user != nil {
		role := ""
		if m.user.Role == models.RoleAdmin {
			role = okStyle.Render(" [admin]")
		}
		out += labelStyle.Render(fmt.Sprintf("%s%s", m.user.DisplayName, role))
	}
	out += "\n\n"
	for i, it := range m.menuItems {
		cursor := "  "
		label := labelStyle.Render(it.label)
		if i == m.menuCursor {
			cursor = cursorOn.Render("▸ ")
			label = cursorOn.Render(it.label)
		}
		out += cursor + label + "\n"
	}
	out += "\n" + statusLine(m) + helpStyle.Render("↑/↓: move · enter: select · q: quit")
	return out
}
