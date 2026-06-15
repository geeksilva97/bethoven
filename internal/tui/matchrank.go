package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/models"
	"bethoven/internal/service"
)

// updateMatchRank has three modes: picking a match from the fixtures list; then
// (rankMatch set) browsing that game's ranking, where you can filter/select a
// player; then (rankBreakdown set) viewing one player's points breakdown.
func (m Model) updateMatchRank(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	// Breakdown mode: read-only panel for one player; b/esc returns to ranking.
	if m.rankBreakdown != nil {
		if key.String() == "q" {
			return m, tea.Quit
		}
		m.rankBreakdown = nil
		return m, nil
	}

	// Ranking mode: browse/filter players, enter drills into the breakdown.
	if m.rankMatch != nil {
		return m.updateRankRows(key)
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

// updateRankRows drives the ranking list: arrow/search navigation over players,
// enter opens the highlighted player's breakdown, b/esc returns to the match list.
func (m Model) updateRankRows(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	vis := filterStandings(m.rankRows, m.rankPlayerSearch.query())

	if m.rankPlayerSearch.active {
		switch key.String() {
		case "esc":
			m.rankPlayerSearch.close()
			m.rankCursor = 0
			return m, nil
		case "enter":
			return m.openBreakdown(vis), nil
		case "up":
			if m.rankCursor > 0 {
				m.rankCursor--
			}
			return m, nil
		case "down":
			if m.rankCursor < len(vis)-1 {
				m.rankCursor++
			}
			return m, nil
		}
		cmd := m.rankPlayerSearch.update(key)
		if n := len(filterStandings(m.rankRows, m.rankPlayerSearch.query())); m.rankCursor >= n {
			m.rankCursor = n - 1
		}
		if m.rankCursor < 0 {
			m.rankCursor = 0
		}
		return m, cmd
	}

	switch key.String() {
	case "q":
		return m, tea.Quit
	case "esc", "b":
		m.rankMatch, m.rankRows = nil, nil
		m.rankPlayerSearch.close()
		m.rankCursor = 0
		return m, nil
	case "/":
		return m, m.rankPlayerSearch.open()
	case "up", "k":
		if m.rankCursor > 0 {
			m.rankCursor--
		}
	case "down", "j":
		if m.rankCursor < len(vis)-1 {
			m.rankCursor++
		}
	case "enter":
		return m.openBreakdown(vis), nil
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
	m.rankCursor, m.rankBreakdown = 0, nil
	m.rankPlayerSearch = newSearchBox("filter players…")
	return m, nil
}

// openBreakdown selects the highlighted player and switches to the breakdown panel.
func (m Model) openBreakdown(vis []service.MatchStanding) Model {
	if len(vis) == 0 {
		return m
	}
	r := vis[m.rankCursor]
	m.rankBreakdown = &r
	m.rankPlayerSearch.close()
	return m
}

// filterStandings keeps only the rows whose player name hits the (lower-cased)
// query; returns the input unchanged when the query is empty.
func filterStandings(in []service.MatchStanding, q string) []service.MatchStanding {
	if q == "" {
		return in
	}
	out := make([]service.MatchStanding, 0, len(in))
	for _, r := range in {
		if strings.Contains(strings.ToLower(r.User.DisplayName), q) {
			out = append(out, r)
		}
	}
	return out
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

	if m.rankBreakdown != nil {
		return m.viewBreakdown(*m.rankMatch, *m.rankBreakdown)
	}

	// ranking mode
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
		out += "\n" + helpStyle.Render("b: back to games · q: quit")
		return out
	}
	if len(m.rankRows) == 0 {
		out += helpStyle.Render("Nobody bet on this match.\n")
		out += "\n" + helpStyle.Render("b: back to games · q: quit")
		return out
	}

	out += m.rankPlayerSearch.view()
	vis := filterStandings(m.rankRows, m.rankPlayerSearch.query())
	out += labelStyle.Render(fmt.Sprintf("  %-20s %-8s %s", "player", "pick", "pts")) + "\n"
	if len(vis) == 0 {
		out += helpStyle.Render("  no players.\n")
	} else {
		rows := make([]string, len(vis))
		for i, r := range vis {
			line := fmt.Sprintf("  %-20s %-8s %d", r.User.DisplayName, fmtPick(r.Bet), r.Points)
			if i == m.rankCursor {
				line = selBar.Render(line)
			} else {
				line = labelStyle.Render(line)
			}
			rows[i] = line
		}
		for _, r := range windowRows(rows, m.rankCursor, m.listCapacity()) {
			out += r + "\n"
		}
	}
	help := "↑/↓: move · enter: breakdown · /: search · b: back · q: quit"
	if m.rankPlayerSearch.active {
		help = "type to filter · ↑/↓: move · enter: breakdown · esc: clear"
	}
	out += "\n" + helpStyle.Render(help)
	return out
}

// viewBreakdown is the per-player drill-down: how this player's points on this
// match were earned, line by line (base + any contrarian bonuses).
func (m Model) viewBreakdown(mt models.Match, r service.MatchStanding) string {
	out := titleStyle.Render(fmt.Sprintf("%s v %s", withFlag(mt.TeamA), withFlag(mt.TeamB)))
	out += labelStyle.Render("   result: "+fmtResult(mt)) + "\n\n"
	out += cursorOn.Render(fmt.Sprintf("  %s — pick %s · %d pts",
		r.User.DisplayName, fmtPick(r.Bet), r.Points)) + "\n\n"

	for _, l := range r.Breakdown.Lines {
		line := fmt.Sprintf("  %-22s %+d", l.Label, l.Points)
		out += okStyle.Render(line)
		if l.Note != "" {
			out += helpStyle.Render("   " + l.Note)
		}
		out += "\n"
	}
	out += labelStyle.Render(fmt.Sprintf("  %-22s %d", "Total", r.Breakdown.Total)) + "\n"

	out += "\n" + helpStyle.Render("any key: back to ranking · q: quit")
	return out
}
