package tui

import (
	"fmt"
	"strings"

	"bethoven/internal/models"
)

// listChrome is the rough number of non-list lines on a list screen (title,
// spacing, status, help). Used to size the scroll window so the help line and
// cursor never slide off the bottom of the terminal.
const listChrome = 7

// listCapacity is how many fixture rows we can show given the terminal height.
// Before the first WindowSizeMsg height is 0, so fall back to a sane default.
func (m Model) listCapacity() int {
	if m.height <= 0 {
		return 20
	}
	if n := m.height - listChrome; n >= 3 {
		return n
	}
	return 3
}

// windowRows returns the rows visible around cursor, capped to maxRows. When the
// list is taller than the window it keeps the cursor roughly centered and
// overlays "↑ N more" / "↓ N more" markers on the clipped edges (never on the
// cursor's own row). Output height is always min(len(rows), maxRows).
func windowRows(rows []string, cursor, maxRows int) []string {
	if maxRows < 1 {
		maxRows = 1
	}
	if len(rows) <= maxRows {
		return rows
	}

	start := cursor - maxRows/2
	if start < 0 {
		start = 0
	}
	if start > len(rows)-maxRows {
		start = len(rows) - maxRows
	}
	end := start + maxRows

	out := make([]string, 0, maxRows)
	for i := start; i < end; i++ {
		out = append(out, rows[i])
	}
	if start > 0 && cursor != start {
		out[0] = helpStyle.Render(fmt.Sprintf("  ↑ %d more", start))
	}
	if end < len(rows) && cursor != end-1 {
		out[len(out)-1] = helpStyle.Render(fmt.Sprintf("  ↓ %d more", len(rows)-end))
	}
	return out
}

// renderList renders match rows for a list screen: builds one matchLine per
// match, scrolls to keep the cursor visible, and joins with newlines (trailing
// newline included so callers can append their footer directly).
func (m Model) renderList(matches []models.Match, cursor int) string {
	rows := make([]string, len(matches))
	for i, mt := range matches {
		rows[i] = m.matchLine(mt, i == cursor)
	}
	visible := windowRows(rows, cursor, m.listCapacity())
	return strings.Join(visible, "\n") + "\n"
}
