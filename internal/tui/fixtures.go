package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/models"
)

// todayWindow is how far ahead the default fixtures view looks (the next 48h).
const todayWindow = 48 * time.Hour

// matchLine renders one fixture row for a list, marking lock state. The selected
// row is drawn as a solid gold bar so it's unmistakable on any terminal.
func (m Model) matchLine(mt models.Match, selected bool) string {
	when := fmtKickoff(mt.StartsAt)
	label := fmt.Sprintf("%s v %s  %s", teamCell(mt.TeamA, 17), teamCell(mt.TeamB, 17), when)
	if mt.GroupLabel != "" {
		label += "  " + mt.GroupLabel
	}

	locked := !m.svc.Now().Before(mt.StartsAt.UTC())
	tag := ""
	switch {
	case mt.Finished && mt.ScoreA != nil:
		tag = fmt.Sprintf("  [%d-%d]", *mt.ScoreA, *mt.ScoreB)
	case locked:
		tag = "  🔒 locked"
	}

	if selected {
		// One uniform bar covers cursor + label + tag.
		return selBar.Render("▸ " + label + tag)
	}

	styledTag := ""
	switch {
	case mt.Finished && mt.ScoreA != nil:
		styledTag = okStyle.Render(tag)
	case locked:
		styledTag = lockStyle.Render(tag)
	}
	return "  " + labelStyle.Render(label) + styledTag
}

// resetFixFilter clears the fixtures view back to its default (next-24h window,
// no search). Called whenever the bet screen is (re)entered from the menu.
func (m *Model) resetFixFilter() {
	m.fixShowAll = false
	m.fixSearch = newSearchBox("filter teams…")
	m.fixCursor = 0
}

// visibleFixtures applies the active filters to m.fixtures: the next-24h window
// (unless fixShowAll), then the search query. Both updateFixtures and
// viewFixtures call this, so the displayed list and the cursor target never drift.
func (m Model) visibleFixtures() []models.Match {
	list := m.fixtures
	if !m.fixShowAll {
		now := m.svc.Now()
		var window []models.Match
		for _, mt := range list {
			s := mt.StartsAt.UTC()
			if !s.Before(now) && s.Before(now.Add(todayWindow)) {
				window = append(window, mt)
			}
		}
		if len(window) == 0 {
			// Never-empty fallback: the next handful of upcoming matches.
			for _, mt := range list {
				if !mt.StartsAt.UTC().Before(now) {
					window = append(window, mt)
					if len(window) >= 10 {
						break
					}
				}
			}
		}
		list = window
	}

	return filterMatches(list, m.fixSearch.query())
}

// clampFixCursor keeps the cursor within the (possibly shrunken) visible list.
func (m *Model) clampFixCursor() {
	n := len(m.visibleFixtures())
	if m.fixCursor >= n {
		m.fixCursor = n - 1
	}
	if m.fixCursor < 0 {
		m.fixCursor = 0
	}
}

func (m Model) updateFixtures(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	vis := m.visibleFixtures()

	// Search mode: most keys edit the query; arrows still move; enter bets.
	if m.fixSearch.active {
		switch key.String() {
		case "esc":
			m.fixSearch.close()
			m.fixCursor = 0
			return m, nil
		case "enter":
			if len(vis) == 0 {
				return m, nil
			}
			return m.betFromList(vis)
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
		cmd := m.fixSearch.update(msg)
		m.clampFixCursor()
		return m, cmd
	}

	switch key.String() {
	case "q":
		return m, tea.Quit
	case "esc", "b":
		return m.goMenu(), nil
	case "/":
		return m, m.fixSearch.open()
	case "a":
		m.fixShowAll = !m.fixShowAll
		m.fixCursor = 0
		return m, nil
	case "up", "k":
		if m.fixCursor > 0 {
			m.fixCursor--
		}
	case "down", "j":
		if m.fixCursor < len(vis)-1 {
			m.fixCursor++
		}
	case "enter":
		if len(vis) == 0 {
			return m, nil
		}
		return m.betFromList(vis)
	}
	return m, nil
}

// betFromList opens the bet form for the highlighted match, rejecting matches
// whose betting window has closed (belt-and-suspenders; the service enforces it too).
func (m Model) betFromList(vis []models.Match) (tea.Model, tea.Cmd) {
	mt := vis[m.fixCursor]
	if mt.Finished || !m.svc.Now().Before(mt.StartsAt.UTC()) {
		m.setStatus("that match is closed — betting ended at kickoff", true)
		return m, nil
	}
	return m.openBet(mt), textinput.Blink
}

func (m Model) viewFixtures() string {
	vis := m.visibleFixtures()

	scope := "next 48h · a: all"
	if m.fixShowAll {
		scope = "all · a: 48h"
	}
	out := titleStyle.Render("Fixtures") + labelStyle.Render("  ("+scope+")") + "\n\n"

	out += m.fixSearch.view()

	if len(vis) == 0 {
		out += helpStyle.Render("  no matches.\n")
	} else {
		out += m.renderList(vis, m.fixCursor)
	}

	help := "↑/↓: move · enter: bet · /: search · a: today/all · b: back · q: quit"
	if m.fixSearch.active {
		help = "type to filter · ↑/↓: move · enter: bet · esc: clear"
	}
	out += "\n" + statusLine(m) + helpStyle.Render(help)
	return out
}
