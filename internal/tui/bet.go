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
	m.betOver = false

	a, b := "", ""
	if existing, _ := m.svc.MyBet(m.user.ID, mt.ID); existing != nil {
		a = strconv.Itoa(existing.PredA)
		b = strconv.Itoa(existing.PredB)
		m.betOver = existing.BonusOver
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
		return m.goMenu(), nil
	case "tab", "down", "right":
		m.betFocus = (m.betFocus + 1) % 2
		m.focusBet()
		return m, nil
	case "shift+tab", "up", "left":
		m.betFocus = (m.betFocus + 1) % 2
		m.focusBet()
		return m, nil
	case " ", "o":
		m.betOver = !m.betOver
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
	if err := m.svc.PlaceBet(m.user.ID, m.betMatch.ID, int64(a), int64(b), m.betOver); err != nil {
		m.setStatus(err.Error(), true)
		return m, nil
	}
	mdl := m.goMenu()
	mdl.setStatus(fmt.Sprintf("pick saved: %s %d-%d %s", m.betMatch.TeamA, a, b, overLabel(m.betOver)), false)
	return mdl, nil
}

func overLabel(over bool) string {
	if over {
		return "(over 2.5)"
	}
	return "(under 2.5)"
}

func (m Model) viewBet() string {
	mt := m.betMatch
	out := titleStyle.Render(fmt.Sprintf("%s  v  %s", mt.TeamA, mt.TeamB)) + "\n"
	out += labelStyle.Render(mt.StartsAt.UTC().Format("Mon 02 Jan 15:04 UTC"))
	if mt.GroupLabel != "" {
		out += labelStyle.Render("  ·  " + mt.GroupLabel)
	}
	out += "\n\n"

	out += "  " + m.betScoreField(0, mt.TeamA) + "   " + m.betScoreField(1, mt.TeamB) + "\n\n"

	out += "  " + labelStyle.Render("Total goals:") + "  " + overOption(m.betOver) + "\n\n"
	out += statusLine(m) + helpStyle.Render("type scores · tab: switch · space: toggle over/under · enter: save · b: back")
	return out
}

func (m Model) betScoreField(i int, team string) string {
	box := m.betInputs[i].View()
	if i == m.betFocus {
		box = cursorOn.Render("[" + box + "]")
	} else {
		box = "[" + box + "]"
	}
	return labelStyle.Render(team) + " " + box
}

func overOption(over bool) string {
	o, u := "Over 2.5", "Under 2.5"
	if over {
		return cursorOn.Render("("+o+")") + "  " + labelStyle.Render(u)
	}
	return labelStyle.Render(o) + "  " + cursorOn.Render("("+u+")")
}
