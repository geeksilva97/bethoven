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

// lineCount returns how many terminal rows a rendered string occupies (0 for the
// empty string). A trailing newline doesn't add a visible content row.
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n") + 1
	if strings.HasSuffix(s, "\n") {
		n--
	}
	return n
}

// windowBlocks is the visual-line analogue of windowRows: it windows multi-line
// blocks (each rendered string may span several rows) to fit within maxLines
// terminal rows, keeping block `cursor` visible and overlaying "↑ N more"/"↓ N more"
// on the clipped edges. Markers are counted against the budget (not added on top),
// so the result is always ≤ maxLines rows — which is what keeps a tall feed from
// scrolling the screen's title off the top. Single-line blocks reduce to windowRows.
func windowBlocks(blocks []string, cursor, maxLines int) string {
	if maxLines < 1 {
		maxLines = 1
	}
	if len(blocks) == 0 {
		return ""
	}
	h := make([]int, len(blocks))
	total := 0
	for i, b := range blocks {
		if h[i] = lineCount(b); h[i] < 1 {
			h[i] = 1
		}
		total += h[i]
	}
	if total <= maxLines {
		return strings.Join(blocks, "\n")
	}

	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(blocks) {
		cursor = len(blocks) - 1
	}

	// Reserve up to two rows for the edge markers; always show at least the cursor
	// block, then grow downward, then upward, while the next block still fits.
	budget := maxLines - 2
	if budget < 1 {
		budget = 1
	}
	start, end, used := cursor, cursor+1, h[cursor]
	for {
		grew := false
		if end < len(blocks) && used+h[end] <= budget {
			used += h[end]
			end++
			grew = true
		}
		if start > 0 && used+h[start-1] <= budget {
			start--
			used += h[start]
			grew = true
		}
		if !grew {
			break
		}
	}

	var b strings.Builder
	if start > 0 {
		b.WriteString(helpStyle.Render(fmt.Sprintf("  ↑ %d more", start)))
		b.WriteString("\n")
	}
	for i := start; i < end; i++ {
		b.WriteString(blocks[i])
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	if end < len(blocks) {
		b.WriteString("\n")
		b.WriteString(helpStyle.Render(fmt.Sprintf("  ↓ %d more", len(blocks)-end)))
	}
	return b.String()
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
