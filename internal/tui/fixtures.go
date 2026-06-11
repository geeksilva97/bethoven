package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/models"
)

// todayWindow is how far ahead the default ("today") fixtures view looks.
const todayWindow = 24 * time.Hour

// matchLine renders one fixture row for a list, marking lock state. The selected
// row is drawn as a solid gold bar so it's unmistakable on any terminal.
func (m Model) matchLine(mt models.Match, selected bool) string {
	when := mt.StartsAt.UTC().Format("Mon 02 Jan 15:04")
	label := fmt.Sprintf("%-14s v %-14s  %s", mt.TeamA, mt.TeamB, when)
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

// newSearchInput builds the fixtures filter box.
func newSearchInput() textinput.Model {
	in := textinput.New()
	in.Placeholder = "filter teams…"
	in.Prompt = "/ "
	in.PromptStyle = cursorOn
	in.Cursor.Style = cursorOn
	return in
}

// resetFixFilter clears the fixtures view back to its default (next-24h window,
// no search). Called whenever the bet screen is (re)entered from the menu.
func (m *Model) resetFixFilter() {
	m.fixShowAll = false
	m.fixSearching = false
	m.fixSearch = newSearchInput()
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

	q := strings.TrimSpace(strings.ToLower(m.fixSearch.Value()))
	if q == "" {
		return list
	}
	var out []models.Match
	for _, mt := range list {
		hay := strings.ToLower(mt.TeamA + " " + mt.TeamB + " " + mt.GroupLabel)
		if strings.Contains(hay, q) {
			out = append(out, mt)
		}
	}
	return out
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
	if m.fixSearching {
		switch key.String() {
		case "esc":
			m.fixSearching = false
			m.fixSearch.SetValue("")
			m.fixSearch.Blur()
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
		var cmd tea.Cmd
		m.fixSearch, cmd = m.fixSearch.Update(msg)
		m.clampFixCursor()
		return m, cmd
	}

	switch key.String() {
	case "q":
		return m, tea.Quit
	case "esc", "b":
		return m.goMenu(), nil
	case "/":
		m.fixSearching = true
		m.fixSearch.Focus()
		return m, textinput.Blink
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

	scope := "today · a: all"
	if m.fixShowAll {
		scope = "all · a: today"
	}
	out := titleStyle.Render("Fixtures") + labelStyle.Render("  ("+scope+")") + "\n\n"

	if m.fixSearching || m.fixSearch.Value() != "" {
		out += "  " + m.fixSearch.View() + "\n\n"
	}

	if len(vis) == 0 {
		out += helpStyle.Render("  no matches.\n")
	} else {
		out += m.renderList(vis, m.fixCursor)
	}

	help := "↑/↓: move · enter: bet · /: search · a: today/all · b: back · q: quit"
	if m.fixSearching {
		help = "type to filter · ↑/↓: move · enter: bet · esc: clear"
	}
	out += "\n" + statusLine(m) + helpStyle.Render(help)
	return out
}
