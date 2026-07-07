package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/achievements"
)

// updateTrophies drives the read-only achievements board: ↑↓/jk move the badge
// cursor (which scrolls the window), esc returns to the menu, q quits.
func (m Model) updateTrophies(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "q":
		return m, tea.Quit
	case "up", "k":
		if m.trophyCursor > 0 {
			m.trophyCursor--
		}
	case "down", "j":
		if m.trophyCursor < len(m.trophies.Standings)-1 {
			m.trophyCursor++
		}
	case "esc", "enter":
		return m.goMenu(), nil
	}
	return m, nil
}

// viewTrophies renders the badge board: every badge in catalog order with its
// current holder(s), unclaimed superlatives dimmed — a target to chase. Badges
// derive from finished matches only, so nothing here leaks an upcoming pick.
func (m Model) viewTrophies() string {
	out := titleStyle.Render("🏅  Achievements") + "\n\n"

	blocks := make([]string, len(m.trophies.Standings))
	for i, st := range m.trophies.Standings {
		blocks[i] = m.trophyBlock(st, i == m.trophyCursor)
	}
	out += windowBlocks(blocks, m.trophyCursor, m.listCapacity())
	out += "\n\n" + statusLine(m) + helpStyle.Render("↑/↓: move · esc: back · q: quit")
	return out
}

// trophyBlock renders one badge and its holders as a multi-line block.
func (m Model) trophyBlock(st achievements.BadgeStanding, selected bool) string {
	cursor := "  "
	title := st.Badge.Emoji + " " + st.Badge.Name
	head := labelStyle.Render(title)
	if selected {
		cursor = cursorOn.Render("▸ ")
		head = cursorOn.Render(title)
	}
	var b strings.Builder
	b.WriteString(cursor + head + helpStyle.Render(" · "+st.Badge.Desc))
	if len(st.Holders) == 0 {
		b.WriteString("\n      " + helpStyle.Render("— unclaimed —"))
		return b.String()
	}
	for _, h := range st.Holders {
		b.WriteString("\n      " + okStyle.Render(h.Name) + helpStyle.Render(" — "+h.Detail))
	}
	return b.String()
}
