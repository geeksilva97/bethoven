package tui

import (
	"fmt"
	"strconv"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/models"
)

// openBet sets up the bet form for a match, pre-filling any existing pick.
func (m Model) openBet(mt models.Match) Model {
	m.betMatch = mt
	m.betFocus = 0

	a, b := "", ""
	if existing, _ := m.svc.MyBet(m.user.ID, mt.ID); existing != nil {
		a = strconv.Itoa(existing.PredA)
		b = strconv.Itoa(existing.PredB)
	}
	ia, ib := scoreInput(a), scoreInput(b)
	ia.Focus()
	m.betInputs = []textinput.Model{ia, ib}
	m.screen = screenBet
	m.status = ""
	return m
}

func scoreInput(val string) textinput.Model {
	in := textinput.New()
	in.CharLimit = 2
	in.Prompt = ""
	in.Cursor.Style = cursorOn // visible gold caret in the focused field
	in.SetValue(val)
	return in
}

func (m Model) updateBet(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		m.betInputs[m.betFocus], cmd = m.betInputs[m.betFocus].Update(msg)
		return m, cmd
	}

	switch key.String() {
	case "esc", "b":
		// Back to the fixtures list we opened this from, not the main menu.
		m.screen = screenFixtures
		m.status = ""
		return m, nil
	case "tab", "down", "right":
		m.betFocus = (m.betFocus + 1) % 2
		m.focusBet()
		return m, nil
	case "shift+tab", "up", "left":
		m.betFocus = (m.betFocus + 1) % 2
		m.focusBet()
		return m, nil
	case "enter":
		return m.submitBet()
	}
	var cmd tea.Cmd
	m.betInputs[m.betFocus], cmd = m.betInputs[m.betFocus].Update(msg)
	return m, cmd
}

func (m *Model) focusBet() {
	for i := range m.betInputs {
		if i == m.betFocus {
			m.betInputs[i].Focus()
		} else {
			m.betInputs[i].Blur()
		}
	}
}

func (m Model) submitBet() (tea.Model, tea.Cmd) {
	a, errA := strconv.Atoi(m.betInputs[0].Value())
	b, errB := strconv.Atoi(m.betInputs[1].Value())
	if errA != nil || errB != nil {
		m.setStatus("enter whole numbers for both scores", true)
		return m, nil
	}
	if err := m.svc.PlaceBet(m.user.ID, m.betMatch.ID, int64(a), int64(b)); err != nil {
		m.setStatus(err.Error(), true)
		return m, nil
	}
	// Back to the fixtures list (where we came from), not all the way to the
	// menu — cursor and filters are still in m, so the user lands right where
	// they were, with a confirmation.
	m.screen = screenFixtures
	m.setStatus(fmt.Sprintf("pick saved: %s %d-%d %s", m.betMatch.TeamA, a, b, m.betMatch.TeamB), false)
	return m, nil
}

func (m Model) viewBet() string {
	mt := m.betMatch
	out := titleStyle.Render(fmt.Sprintf("%s  v  %s", withFlag(mt.TeamA), withFlag(mt.TeamB))) + "\n"
	out += labelStyle.Render(fmtKickoff(mt.StartsAt))
	if mt.GroupLabel != "" {
		out += labelStyle.Render("  ·  " + mt.GroupLabel)
	}
	out += "\n\n"

	out += "  " + m.betScoreField(0, mt.TeamA) + "   " + m.betScoreField(1, mt.TeamB) + "\n\n"

	out += statusLine(m) + helpStyle.Render("type scores · tab: switch · enter: save · b: back")
	return out
}

func (m Model) betScoreField(i int, team string) string {
	return scoreField(m.betInputs[i], withFlag(team), i == m.betFocus)
}
