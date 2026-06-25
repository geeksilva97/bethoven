package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/models"
	"bethoven/internal/service"
)

// phaseCycle is the order the admin toggles through when adding a match.
var phaseCycle = []models.Phase{
	models.PhaseGroup, models.PhaseRound32, models.PhaseRound16, models.PhaseRound8, models.PhaseSemi, models.PhaseFinal,
}

// --- add match -----------------------------------------------------------

func (m *Model) initAddMatch() {
	mk := func(ph string) textinput.Model {
		in := textinput.New()
		in.Placeholder = ph
		return in
	}
	teamA := mk("team A")
	teamA.Focus()
	m.addInputs = []textinput.Model{
		teamA,
		mk("team B"),
		mk("group (optional, e.g. Group G)"),
		mk("kickoff (local tz), e.g. 2026-06-20 18:00"),
	}
	m.addFocus = 0
	m.addPhase = models.PhaseRound32 // the first knockout round (48-team format) is the common admin add
	m.focusAdd()
}

func (m Model) updateAddMatch(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		m.addInputs[m.addFocus], cmd = m.addInputs[m.addFocus].Update(msg)
		return m, cmd
	}
	switch key.String() {
	case "esc":
		return m.goMenu(), nil
	case "tab", "down":
		m.addFocus = (m.addFocus + 1) % len(m.addInputs)
		m.focusAdd()
		return m, nil
	case "shift+tab", "up":
		m.addFocus = (m.addFocus - 1 + len(m.addInputs)) % len(m.addInputs)
		m.focusAdd()
		return m, nil
	case "ctrl+p":
		m.addPhase = nextPhase(m.addPhase)
		return m, nil
	case "enter":
		return m.submitAddMatch()
	}
	var cmd tea.Cmd
	m.addInputs[m.addFocus], cmd = m.addInputs[m.addFocus].Update(msg)
	return m, cmd
}

// focusAdd moves focus to m.addFocus and restyles the inputs so the active one
// is gold with a visible caret while the rest stay dim (matching focusReg).
func (m *Model) focusAdd() {
	for i := range m.addInputs {
		if i == m.addFocus {
			m.addInputs[i].Focus()
			m.addInputs[i].PromptStyle = cursorOn
			m.addInputs[i].TextStyle = cursorOn
			m.addInputs[i].Cursor.Style = cursorOn
		} else {
			m.addInputs[i].Blur()
			m.addInputs[i].PromptStyle = helpStyle
			m.addInputs[i].TextStyle = labelStyle
		}
		m.addInputs[i].PlaceholderStyle = helpStyle
	}
}

func nextPhase(p models.Phase) models.Phase {
	for i, ph := range phaseCycle {
		if ph == p {
			return phaseCycle[(i+1)%len(phaseCycle)]
		}
	}
	return phaseCycle[0]
}

func (m Model) submitAddMatch() (tea.Model, tea.Cmd) {
	teamA := m.addInputs[0].Value()
	teamB := m.addInputs[1].Value()
	group := m.addInputs[2].Value()
	// Admin enters kickoff in the display timezone; store it as UTC.
	startsAt, err := time.ParseInLocation("2006-01-02 15:04", m.addInputs[3].Value(), displayLoc)
	if err != nil {
		m.setStatus("kickoff must look like 2026-06-20 18:00", true)
		return m, nil
	}
	if teamA == "" || teamB == "" {
		m.setStatus("both team names are required", true)
		return m, nil
	}
	if _, err := m.svc.AddMatch(m.user, teamA, teamB, m.addPhase, group, startsAt.UTC()); err != nil {
		m.setStatus(err.Error(), true)
		return m, nil
	}
	mdl := m.goMenu()
	mdl.setStatus(fmt.Sprintf("added %s v %s (%s)", teamA, teamB, m.addPhase), false)
	return mdl, nil
}

func (m Model) viewAddMatch() string {
	out := titleStyle.Render("⚙  Add match") + "\n\n"
	labels := []string{"Team A", "Team B", "Group", "Kickoff"}
	for i := range m.addInputs {
		cur := "  "
		if i == m.addFocus {
			cur = cursorOn.Render("▸ ")
		}
		out += cur + labelStyle.Render(fmt.Sprintf("%-8s ", labels[i])) + m.addInputs[i].View() + "\n"
	}
	out += "\n  " + labelStyle.Render("Phase: ") + cursorOn.Render(string(m.addPhase)) +
		helpStyle.Render("  (ctrl+p to cycle)") + "\n\n"
	out += statusLine(m) + helpStyle.Render("tab: field · ctrl+p: phase · enter: add · esc: back")
	return out
}

// --- enter result --------------------------------------------------------

func (m Model) updateEnterResult(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		if m.resMatch != nil {
			var cmd tea.Cmd
			m.resInputs[m.resFocus], cmd = m.resInputs[m.resFocus].Update(msg)
			return m, cmd
		}
		return m, nil
	}

	// Score-entry mode.
	if m.resMatch != nil {
		switch key.String() {
		case "esc":
			m.resMatch = nil
			return m, nil
		case "tab", "up", "down", "left", "right":
			m.resFocus = (m.resFocus + 1) % 2
			for i := range m.resInputs {
				if i == m.resFocus {
					m.resInputs[i].Focus()
				} else {
					m.resInputs[i].Blur()
				}
			}
			return m, nil
		case "enter":
			return m.submitResult()
		}
		var cmd tea.Cmd
		m.resInputs[m.resFocus], cmd = m.resInputs[m.resFocus].Update(msg)
		return m, cmd
	}

	// Match-pick mode.
	vis := filterMatches(m.fixtures, m.resSearch.query())

	if m.resSearch.active {
		switch key.String() {
		case "esc":
			m.resSearch.close()
			m.resCursor = 0
			return m, nil
		case "enter":
			if len(vis) == 0 {
				return m, nil
			}
			return m.pickResult(vis)
		case "up":
			if m.resCursor > 0 {
				m.resCursor--
			}
			return m, nil
		case "down":
			if m.resCursor < len(vis)-1 {
				m.resCursor++
			}
			return m, nil
		}
		cmd := m.resSearch.update(msg)
		if n := len(filterMatches(m.fixtures, m.resSearch.query())); m.resCursor >= n {
			m.resCursor = n - 1
		}
		if m.resCursor < 0 {
			m.resCursor = 0
		}
		return m, cmd
	}

	switch key.String() {
	case "q":
		return m, tea.Quit
	case "esc", "b":
		return m.goMenu(), nil
	case "/":
		return m, m.resSearch.open()
	case "up", "k":
		if m.resCursor > 0 {
			m.resCursor--
		}
	case "down", "j":
		if m.resCursor < len(vis)-1 {
			m.resCursor++
		}
	case "enter":
		if len(vis) == 0 {
			return m, nil
		}
		return m.pickResult(vis)
	}
	return m, nil
}

// pickResult opens the score-entry form for the highlighted match, pre-filling
// any existing result.
func (m Model) pickResult(vis []models.Match) (tea.Model, tea.Cmd) {
	mt := vis[m.resCursor]
	m.resMatch = &mt
	a, b := scoreInput(""), scoreInput("")
	if mt.Finished && mt.ScoreA != nil {
		a.SetValue(strconv.Itoa(*mt.ScoreA))
		b.SetValue(strconv.Itoa(*mt.ScoreB))
	}
	a.Focus()
	m.resInputs = []textinput.Model{a, b}
	m.resFocus = 0
	return m, textinput.Blink
}

func (m Model) submitResult() (tea.Model, tea.Cmd) {
	a, errA := strconv.Atoi(m.resInputs[0].Value())
	b, errB := strconv.Atoi(m.resInputs[1].Value())
	if errA != nil || errB != nil {
		m.setStatus("enter whole numbers for both scores", true)
		return m, nil
	}
	if err := m.svc.EnterResult(m.user, m.resMatch.ID, a, b); err != nil {
		m.setStatus(err.Error(), true)
		return m, nil
	}
	team := fmt.Sprintf("%s %d-%d %s", m.resMatch.TeamA, a, b, m.resMatch.TeamB)
	m.resMatch = nil
	mdl := m.goMenu()
	mdl.setStatus("result recorded: "+team, false)
	return mdl, nil
}

func (m Model) viewEnterResult() string {
	if m.resMatch == nil {
		out := titleStyle.Render("⚙  Enter result") + labelStyle.Render("  (pick a match)") + "\n\n"
		out += m.resSearch.view()
		vis := filterMatches(m.fixtures, m.resSearch.query())
		if len(vis) == 0 {
			out += helpStyle.Render("  no matches.\n")
		} else {
			out += m.renderList(vis, m.resCursor)
		}
		help := "↑/↓: move · enter: pick · /: search · b: back"
		if m.resSearch.active {
			help = "type to filter · ↑/↓: move · enter: pick · esc: clear"
		}
		out += "\n" + statusLine(m) + helpStyle.Render(help)
		return out
	}
	mt := *m.resMatch
	out := titleStyle.Render("⚙  Result: "+withFlag(mt.TeamA)+" v "+withFlag(mt.TeamB)) + "\n"
	out += helpStyle.Render("Enter the regulation (90') score — penalties/ET are ignored.") + "\n\n"
	out += "  " + scoreField(m.resInputs[0], withFlag(mt.TeamA), m.resFocus == 0) + "   " +
		scoreField(m.resInputs[1], withFlag(mt.TeamB), m.resFocus == 1) + "\n\n"
	out += statusLine(m) + helpStyle.Render("tab: switch · enter: save · esc: back to list")
	return out
}

// scoreField renders a labelled [ ] score box, highlighting the focused one in
// gold (shared by the bet form and the admin result form).
func scoreField(in textinput.Model, team string, focused bool) string {
	box := "[" + in.View() + "]"
	if focused {
		box = cursorOn.Render(box)
	}
	return labelStyle.Render(team) + " " + box
}

// --- all bets browser ----------------------------------------------------

// visibleGridMatches applies the grid's active filters: the admin grid defaults
// to the same next-3-days window as Place Bets (toggle with "a"), then the
// search query. The public grid is unwindowed — its value is the revealed picks
// on past/kicked-off matches, which a forward-looking window would hide.
func (m Model) visibleGridMatches() []models.Match {
	if m.grid == nil {
		return nil
	}
	list := m.grid.Matches
	if !m.gridPublic && !m.allShowAll {
		list = upcomingWindow(list, m.svc.Now())
	}
	return filterMatches(list, m.allSearch.query())
}

// updateAllBets drives the admin bets browser: a scrollable/searchable grid of
// every player's picks, with an enter-to-drill-down view of one match's picks.
func (m Model) updateAllBets(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	g := m.grid
	if g == nil || len(g.Users) == 0 {
		if key.String() == "q" {
			return m, tea.Quit
		}
		return m.goMenu(), nil
	}

	// Detail mode: one match's picks (read-only list; search filters players).
	if m.allMatch != nil {
		if m.allSearch.active {
			if key.String() == "esc" {
				m.allSearch.close()
				return m, nil
			}
			return m, m.allSearch.update(msg)
		}
		switch key.String() {
		case "q":
			return m, tea.Quit
		case "esc", "b":
			m.allMatch = nil
			m.allSearch.close()
			return m, nil
		case "/":
			return m, m.allSearch.open()
		}
		return m, nil
	}

	// Grid mode: navigate match rows, search filters them.
	vis := m.visibleGridMatches()
	if m.allSearch.active {
		switch key.String() {
		case "esc":
			m.allSearch.close()
			m.allCursor = 0
			return m, nil
		case "enter":
			if len(vis) == 0 {
				return m, nil
			}
			mt := vis[m.allCursor]
			m.allMatch = &mt
			m.allSearch.close()
			return m, nil
		case "up":
			if m.allCursor > 0 {
				m.allCursor--
			}
			return m, nil
		case "down":
			if m.allCursor < len(vis)-1 {
				m.allCursor++
			}
			return m, nil
		}
		cmd := m.allSearch.update(msg)
		if n := len(m.visibleGridMatches()); m.allCursor >= n {
			m.allCursor = n - 1
		}
		if m.allCursor < 0 {
			m.allCursor = 0
		}
		return m, cmd
	}

	switch key.String() {
	case "q":
		return m, tea.Quit
	case "esc", "b":
		return m.goMenu(), nil
	case "/":
		return m, m.allSearch.open()
	case "a":
		// Toggle next-3-days window vs full schedule (admin grid only).
		if !m.gridPublic {
			m.allShowAll = !m.allShowAll
			if m.allShowAll {
				// Full schedule: focus the current game, not the opener.
				m.allCursor = currentMatchIndex(m.visibleGridMatches())
			} else {
				m.allCursor = 0 // the 3-day window already starts at "now"
			}
		}
	case "up", "k":
		if m.allCursor > 0 {
			m.allCursor--
		}
	case "down", "j":
		if m.allCursor < len(vis)-1 {
			m.allCursor++
		}
	case "enter":
		if len(vis) == 0 {
			return m, nil
		}
		mt := vis[m.allCursor]
		m.allMatch = &mt
	}
	return m, nil
}

func (m Model) viewAllBets() string {
	g := m.grid
	if g == nil || len(g.Users) == 0 {
		out := titleStyle.Render(m.gridTitle()) + "\n\n"
		out += helpStyle.Render("No players yet.\n")
		out += "\n" + helpStyle.Render("any key: back · q: quit")
		return out
	}
	if m.allMatch != nil {
		return m.viewBetsForMatch(*m.allMatch)
	}

	hint := "  (enter: see one match's picks)"
	if !m.gridPublic {
		scope := "next 3 days · a: all"
		if m.allShowAll {
			scope = "all · a: 3 days"
		}
		hint = "  (" + scope + ")"
	}
	out := titleStyle.Render(m.gridTitle()) + labelStyle.Render(hint) + "\n\n"
	out += m.allSearch.view()
	vis := m.visibleGridMatches()

	// Fixed header row: match column + one column per player.
	header := fmt.Sprintf("%-26s", "match")
	for _, u := range g.Users {
		header += fmt.Sprintf(" %-10s", trunc(u.DisplayName, 10))
	}
	out += labelStyle.Render(header) + "\n"

	if len(vis) == 0 {
		out += helpStyle.Render("  no matches.\n")
	} else {
		rows := make([]string, len(vis))
		for i, mt := range vis {
			// 11 + " v " (3) + 12 = 26 display cols, matching the header/total rows.
			row := teamCell(mt.TeamA, 11) + " v " + teamCell(mt.TeamB, 12)
			for _, u := range g.Users {
				row += fmt.Sprintf(" %-10s", trunc(betCellText(g, mt, u.ID), 10))
			}
			if i == m.allCursor {
				row = selBar.Render(row)
			}
			rows[i] = row
		}
		for _, r := range windowRows(rows, m.allCursor, m.listCapacity()) {
			out += r + "\n"
		}
	}

	// Fixed totals row.
	totals := fmt.Sprintf("%-26s", "TOTAL")
	for _, u := range g.Users {
		totals += fmt.Sprintf(" %-10d", g.Totals[u.ID])
	}
	out += okStyle.Render(totals) + "\n"

	help := "↑/↓: move · enter: see picks · /: search · b: back · q: quit"
	if !m.gridPublic {
		help = "↑/↓: move · enter: see picks · /: search · a: 3 days/all · b: back · q: quit"
	}
	if m.allSearch.active {
		help = "type to filter · ↑/↓: move · enter: see picks · esc: clear"
	}
	out += "\n" + helpStyle.Render(help)
	return out
}

// viewBetsForMatch is the by-match drill-down: every player's pick for one match.
func (m Model) viewBetsForMatch(mt models.Match) string {
	g := m.grid
	prefix := "⚙  Bets · "
	if m.gridPublic {
		prefix = "Bets · "
	}
	out := titleStyle.Render(prefix+withFlag(mt.TeamA)+" v "+withFlag(mt.TeamB)) + "\n"
	sub := fmtKickoff(mt.StartsAt)
	if mt.GroupLabel != "" {
		sub += "  ·  " + mt.GroupLabel
	}
	if mt.Finished {
		sub += "  ·  result " + fmtResult(mt)
	}
	out += labelStyle.Render(sub) + "\n\n"
	out += m.allSearch.view()

	q := m.allSearch.query()
	var rows []string
	for _, u := range g.Users {
		if q != "" && !strings.Contains(strings.ToLower(u.DisplayName), q) {
			continue
		}
		pick := "—"
		if c, ok := g.Cells[mt.ID][u.ID]; ok {
			pick = fmtPick(c.Bet)
		}
		line := fmt.Sprintf("  %-20s %-8s", trunc(u.DisplayName, 20), pick)
		if mt.Finished {
			if c, ok := g.Cells[mt.ID][u.ID]; ok {
				line += fmt.Sprintf("  %d pts", c.Points)
			}
		}
		rows = append(rows, line)
	}

	if len(rows) == 0 {
		out += helpStyle.Render("  no players.\n")
	} else {
		for _, r := range windowRows(rows, 0, m.listCapacity()) {
			out += r + "\n"
		}
	}

	help := "/: search players · b: back to grid · q: quit"
	if m.allSearch.active {
		help = "type to filter players · esc: clear"
	}
	out += "\n" + helpStyle.Render(help)
	return out
}

// betCellText renders one grid cell: a player's pick on a match, plus points in
// parentheses once the match is finished, or "—" if they didn't bet.
func betCellText(g *service.AllBetsGrid, mt models.Match, userID int64) string {
	c, ok := g.Cells[mt.ID][userID]
	if !ok {
		return "—"
	}
	cell := fmtPick(c.Bet)
	if mt.Finished {
		cell += fmt.Sprintf("(%d)", c.Points)
	}
	return cell
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
