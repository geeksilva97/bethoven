package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/models"
)

// todayWindow is how far ahead the default fixtures view looks (the next 3
// days), so a player betting on Friday sees the whole weekend's matches.
const todayWindow = 72 * time.Hour

// matchLine renders one fixture row for a list, marking lock state. The selected
// row is drawn as a solid gold bar so it's unmistakable on any terminal.
func (m Model) matchLine(mt models.Match, selected bool) string {
	when := fmtKickoff(mt.StartsAt)
	label := fmt.Sprintf("%s v %s  %s", teamCell(mt.TeamA, 17), teamCell(mt.TeamB, 17), when)
	if mt.GroupLabel != "" {
		label += "  " + mt.GroupLabel
	}

	// Round-weight chip: only when the active scheme actually multiplies this
	// match's points (>1) — flat pools and group games show nothing.
	chip := ""
	if w := m.roundWeights.Weight(mt.Phase); w > 1 {
		chip = fmt.Sprintf("  ×%d", w)
	}

	locked := !m.svc.Now().Before(mt.StartsAt.UTC())
	// The "your pick" marker only makes sense on the betting list, where myBets
	// is freshly loaded; other screens reuse matchLine with a stale/empty map.
	bet, hasBet := models.Bet{}, false
	if m.screen == screenFixtures {
		bet, hasBet = m.myBets[mt.ID]
	}

	tag := ""
	switch {
	case mt.Finished && mt.ScoreA != nil:
		tag = fmt.Sprintf("  [%d-%d]", *mt.ScoreA, *mt.ScoreB)
	case mt.Live:
		if mt.LiveClock != "" {
			tag = fmt.Sprintf("  ⚡%s %d-%d", mt.LiveClock, mt.LiveScoreA, mt.LiveScoreB)
		} else {
			tag = fmt.Sprintf("  ⚡ %d-%d", mt.LiveScoreA, mt.LiveScoreB)
		}
	case locked:
		tag = "  🔒 locked"
	case hasBet:
		tag = fmt.Sprintf("  ✓ %d-%d", bet.PredA, bet.PredB)
	}

	if selected {
		// One uniform bar covers cursor + label + chip + tag.
		return selBar.Render("▸ " + label + chip + tag)
	}

	styledTag := ""
	switch {
	case mt.Finished && mt.ScoreA != nil:
		styledTag = okStyle.Render(tag)
	case mt.Live:
		styledTag = liveStyle.Render(tag)
	case locked:
		styledTag = lockStyle.Render(tag)
	case hasBet:
		styledTag = okStyle.Render(tag)
	}
	return "  " + labelStyle.Render(label) + weightStyle.Render(chip) + styledTag
}

// resetFixFilter clears the fixtures view back to its default (next-24h window,
// no search). Called whenever the bet screen is (re)entered from the menu.
func (m *Model) resetFixFilter() {
	m.fixShowAll = false
	m.fixSearch = newSearchBox("filter teams…")
	m.fixCursor = 0
}

// upcomingWindow returns the matches kicking off within todayWindow of now,
// falling back to the next handful of upcoming matches when that window is empty
// so the list is never blank. Shared by the Place Bets screen and the admin bets
// grid as their default (non-"show all") view.
func upcomingWindow(list []models.Match, now time.Time) []models.Match {
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
	return window
}

// currentMatchIndex returns the index a fixtures list should focus on: the match
// happening "now" — the first in-play (live) match, else the first match not yet
// finished (the next kickoff, or a recent game still awaiting its result), else
// the last match when the whole schedule is over. Lists are in kickoff order, so
// finished games cluster at the front and this lands on the past/future frontier.
// Live/Finished already encode time relative to the server clock, so no "now" is
// needed (Live is overlaid with the injected clock; Finished is DB state).
func currentMatchIndex(list []models.Match) int {
	if len(list) == 0 {
		return 0
	}
	// One pass. A live game wins outright; otherwise fall back to the first
	// not-yet-finished game (the next kickoff, or a recent one still awaiting its
	// result). We can't just return that fallback eagerly: a live game can sit
	// after an earlier un-finished-but-not-live game, and the live one should win.
	fallback := -1
	for i, mt := range list {
		if mt.Live { // a game in progress is unambiguously "current"
			return i
		}
		if fallback == -1 && !mt.Finished {
			fallback = i
		}
	}
	if fallback != -1 {
		return fallback
	}
	return len(list) - 1 // everything is over → most recent
}

// visibleFixtures applies the active filters to m.fixtures: the next-3-days
// window (unless fixShowAll), then the search query. Both updateFixtures and
// viewFixtures call this, so the displayed list and the cursor target never drift.
func (m Model) visibleFixtures() []models.Match {
	list := m.fixtures
	if !m.fixShowAll {
		list = upcomingWindow(list, m.svc.Now())
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
		if m.fixShowAll {
			// Full schedule: focus the current game, not the 11 Jun opener.
			m.fixCursor = currentMatchIndex(m.visibleFixtures())
		} else {
			m.fixCursor = 0 // the 3-day window already starts at "now"
		}
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

	scope := "next 3 days · a: all"
	if m.fixShowAll {
		scope = "all · a: 3 days"
	}
	out := titleStyle.Render("Place / edit bets") + labelStyle.Render("  ("+scope+")") + "\n\n"

	out += m.fixSearch.view()

	if len(vis) == 0 {
		out += helpStyle.Render("  no matches.\n")
	} else {
		out += m.renderList(vis, m.fixCursor)
	}

	help := "↑/↓: move · enter: bet · /: search · a: 3 days/all · b: back · q: quit"
	if m.fixSearch.active {
		help = "type to filter · ↑/↓: move · enter: bet · esc: clear"
	}
	out += "\n" + statusLine(m) + helpStyle.Render(help)
	return out
}
