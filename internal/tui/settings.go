package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// gridTitle is the header for the all-bets grid. The player-facing public view
// drops the admin "⚙" framing.
func (m Model) gridTitle() string {
	if m.gridPublic {
		return "All players' bets"
	}
	return "⚙  All bets"
}

// viewSettings renders the admin settings screen: a list of toggles. Only the
// public-bets toggle exists today.
func (m Model) viewSettings() string {
	out := titleStyle.Render("⚙  Admin: settings") + "\n\n"

	state := errStyle.Render("OFF")
	if m.publicBets {
		state = okStyle.Render("ON")
	}
	cursor := "  "
	label := labelStyle.Render("Public bets")
	if m.settingsCursor == 0 {
		cursor = cursorOn.Render("▸ ")
		label = cursorOn.Render("Public bets")
	}
	out += cursor + label + "  " + state + "\n"
	out += helpStyle.Render("    let everyone see others' picks once a match kicks off") + "\n"

	out += "\n" + statusLine(m) + helpStyle.Render("enter/space: toggle · b/esc: back · q: quit")
	return out
}

// updateSettings handles the settings screen: toggle the selected option.
func (m Model) updateSettings(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "q":
		return m, tea.Quit
	case "esc", "b":
		return m.goMenu(), nil
	case "enter", " ":
		if err := m.svc.SetPublicBets(m.user, !m.publicBets); err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.publicBets, _ = m.svc.PublicBetsEnabled()
		if m.publicBets {
			m.setStatus("Public bets enabled — players can now see everyone's picks.", false)
		} else {
			m.setStatus("Public bets disabled.", false)
		}
	}
	return m, nil
}
