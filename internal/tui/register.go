package tui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// initRegister builds the first-connect form. Admins skip the invite code, so
// they get a single name field; everyone else gets code + name.
func (m *Model) initRegister() {
	var inputs []textinput.Model
	if !m.isAdminKey {
		code := textinput.New()
		code.Placeholder = "invite code"
		code.EchoMode = textinput.EchoPassword
		inputs = append(inputs, code)
	}
	name := textinput.New()
	name.Placeholder = "display name"
	name.CharLimit = 32
	inputs = append(inputs, name)

	m.regInputs = inputs
	m.regFocus = 0
	m.focusReg()
}

// codeAndName returns the entered (code, name) accounting for the admin layout.
func (m Model) codeAndName() (code, name string) {
	if m.isAdminKey {
		return "", m.regInputs[0].Value()
	}
	return m.regInputs[0].Value(), m.regInputs[1].Value()
}

func (m Model) updateRegister(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m.updateRegInputs(msg)
	}

	switch key.String() {
	case "esc", "q":
		return m, tea.Quit
	case "tab", "down":
		m.regFocus = (m.regFocus + 1) % len(m.regInputs)
		m.focusReg()
		return m, nil
	case "shift+tab", "up":
		m.regFocus = (m.regFocus - 1 + len(m.regInputs)) % len(m.regInputs)
		m.focusReg()
		return m, nil
	case "enter":
		code, name := m.codeAndName()
		u, err := m.svc.Register(m.fingerprint, code, name)
		if err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.user = u
		return m.goMenu(), nil
	}
	return m.updateRegInputs(msg)
}

// focusReg moves focus to m.regFocus and restyles every field so the active one
// is obviously gold (matching prompt, text, caret) while the rest stay dim.
func (m *Model) focusReg() {
	for i := range m.regInputs {
		if i == m.regFocus {
			m.regInputs[i].Focus()
			m.regInputs[i].PromptStyle = cursorOn
			m.regInputs[i].TextStyle = cursorOn
			m.regInputs[i].Cursor.Style = cursorOn
		} else {
			m.regInputs[i].Blur()
			m.regInputs[i].PromptStyle = helpStyle
			m.regInputs[i].TextStyle = labelStyle
		}
		m.regInputs[i].PlaceholderStyle = helpStyle
	}
}

func (m Model) updateRegInputs(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.regInputs[m.regFocus], cmd = m.regInputs[m.regFocus].Update(msg)
	return m, cmd
}

func (m Model) viewRegister() string {
	out := titleStyle.Render("🎼  BEThoven") + "\n"
	if m.isAdminKey {
		out += okStyle.Render("Admin key recognised — no invite code needed.") + "\n\n"
	} else {
		out += labelStyle.Render("Welcome! You're new here. Enter the invite code and pick a name.") + "\n\n"
	}
	for i := range m.regInputs {
		cursor := "  "
		if i == m.regFocus {
			cursor = cursorOn.Render("▸ ")
		}
		out += cursor + m.regInputs[i].View() + "\n"
	}
	out += "\n" + statusLine(m) + helpStyle.Render("\ntab: next field · enter: join · esc: quit")
	return out
}

// statusLine renders the transient banner if set.
func statusLine(m Model) string {
	if m.status == "" {
		return ""
	}
	if m.statusErr {
		return errStyle.Render("✗ "+m.status) + "\n"
	}
	return okStyle.Render("✓ "+m.status) + "\n"
}
