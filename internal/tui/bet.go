package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/models"
)

// openBet sets up the bet form for a match, pre-filling any existing pick.
func (m Model) openBet(mt models.Match) Model {
	m.betMatch = mt
	m.betFocus = 0
	m.betFormA = m.svc.TeamForm(mt.TeamA)
	m.betFormB = m.svc.TeamForm(mt.TeamB)

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
	// Reflect the new pick in the list marker without a round-trip to the DB.
	if m.myBets == nil {
		m.myBets = make(map[int64]models.Bet)
	}
	m.myBets[m.betMatch.ID] = models.Bet{
		UserID:  m.user.ID,
		MatchID: m.betMatch.ID,
		PredA:   a,
		PredB:   b,
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
	out += "\n"

	// Recent-form strip: TeamA marks left, TeamB right (same order as the header
	// and the score fields below), so no flags/names need repeating.
	out += "  " + labelStyle.Render("last 5  ") + renderForm(m.betFormA) +
		labelStyle.Render("   ·   ") + renderForm(m.betFormB) + "\n"
	if len(m.betFormA) > 0 || len(m.betFormB) > 0 {
		out += "  " + formLegend() + "\n"
	}
	out += "\n"

	out += "  " + m.betScoreField(0, mt.TeamA) + "   " + m.betScoreField(1, mt.TeamB) + "\n\n"

	out += statusLine(m) + helpStyle.Render("type scores · tab: switch · enter: save · b: back")
	return out
}

func (m Model) betScoreField(i int, team string) string {
	return scoreField(m.betInputs[i], withFlag(team), i == m.betFocus)
}

// formLegend explains the last-5 marks (most recent on the right).
func formLegend() string {
	return okStyle.Render("✓") + helpStyle.Render(" win · ") +
		drawStyle.Render("–") + helpStyle.Render(" draw · ") +
		errStyle.Render("✗") + helpStyle.Render(" loss · newest right")
}

// renderForm draws a recent-form strip: ✓ win (green) · – draw (dim) · ✗ loss
// (red), oldest→newest. Shows a dim "—" when no form is known.
func renderForm(form []models.FormOutcome) string {
	if len(form) == 0 {
		return labelStyle.Render("—")
	}
	marks := make([]string, 0, len(form))
	for _, o := range form {
		switch o {
		case models.FormWin:
			marks = append(marks, okStyle.Render("✓"))
		case models.FormDraw:
			marks = append(marks, drawStyle.Render("–"))
		case models.FormLoss:
			marks = append(marks, errStyle.Render("✗"))
		}
	}
	return strings.Join(marks, " ")
}
