package tui

import (
	"fmt"
	"strconv"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/models"
)

// phaseCycle is the order the admin toggles through when adding a match.
var phaseCycle = []models.Phase{
	models.PhaseGroup, models.PhaseRound16, models.PhaseRound8, models.PhaseSemi, models.PhaseFinal,
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
		mk("kickoff UTC, e.g. 2026-06-20 18:00"),
	}
	m.addFocus = 0
	m.addPhase = models.PhaseRound16 // knockouts are the common admin add
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

func (m *Model) focusAdd() {
	for i := range m.addInputs {
		if i == m.addFocus {
			m.addInputs[i].Focus()
		} else {
			m.addInputs[i].Blur()
		}
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
	startsAt, err := time.Parse("2006-01-02 15:04", m.addInputs[3].Value())
	if err != nil {
		m.setStatus("kickoff must look like 2026-06-20 18:00 (UTC)", true)
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
	switch key.String() {
	case "q":
		return m, tea.Quit
	case "esc", "b":
		return m.goMenu(), nil
	case "up", "k":
		if m.resCursor > 0 {
			m.resCursor--
		}
	case "down", "j":
		if m.resCursor < len(m.fixtures)-1 {
			m.resCursor++
		}
	case "enter":
		if len(m.fixtures) == 0 {
			return m, nil
		}
		mt := m.fixtures[m.resCursor]
		m.resMatch = &mt
		a, b := scoreInput(""), scoreInput("")
		if mt.Finished && mt.ScoreA != nil {
			a.SetValue(strconv.Itoa(*mt.ScoreA))
			b.SetValue(strconv.Itoa(*mt.ScoreB))
		}
		a.Focus()
		m.resInputs = []textinput.Model{a, b}
		m.resFocus = 0
	}
	return m, nil
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
		for i, mt := range m.fixtures {
			out += m.matchLine(mt, i == m.resCursor) + "\n"
		}
		out += "\n" + statusLine(m) + helpStyle.Render("↑/↓: move · enter: pick · b: back")
		return out
	}
	mt := *m.resMatch
	out := titleStyle.Render("⚙  Result: "+mt.TeamA+" v "+mt.TeamB) + "\n"
	out += helpStyle.Render("Enter the regulation (90') score — penalties/ET are ignored.") + "\n\n"
	out += "  " + labelStyle.Render(mt.TeamA) + " [" + m.resInputs[0].View() + "]   " +
		labelStyle.Render(mt.TeamB) + " [" + m.resInputs[1].View() + "]\n\n"
	out += statusLine(m) + helpStyle.Render("tab: switch · enter: save · esc: back to list")
	return out
}

// --- all bets grid -------------------------------------------------------

func (m Model) viewAllBets() string {
	g := m.grid
	out := titleStyle.Render("⚙  All bets") + "\n\n"
	if g == nil || len(g.Users) == 0 {
		out += helpStyle.Render("No players yet.\n")
		out += "\n" + helpStyle.Render("any key: back · q: quit")
		return out
	}

	// Header row: match column + one column per player.
	out += labelStyle.Render(fmt.Sprintf("%-22s", "match"))
	for _, u := range g.Users {
		out += labelStyle.Render(fmt.Sprintf(" %-10s", trunc(u.DisplayName, 10)))
	}
	out += "\n"

	for _, mt := range g.Matches {
		name := trunc(fmt.Sprintf("%s v %s", mt.TeamA, mt.TeamB), 22)
		out += fmt.Sprintf("%-22s", name)
		for _, u := range g.Users {
			cell := "—"
			if c, ok := g.Cells[mt.ID][u.ID]; ok {
				cell = fmtPick(c.Bet)
				if mt.Finished {
					cell += fmt.Sprintf("(%d)", c.Points)
				}
			}
			out += fmt.Sprintf(" %-10s", trunc(cell, 10))
		}
		out += "\n"
	}

	// Totals row.
	out += labelStyle.Render(fmt.Sprintf("%-22s", "TOTAL"))
	for _, u := range g.Users {
		out += okStyle.Render(fmt.Sprintf(" %-10d", g.Totals[u.ID]))
	}
	out += "\n\n" + helpStyle.Render("any key: back · q: quit")
	return out
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
