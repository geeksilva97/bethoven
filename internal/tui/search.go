package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/models"
)

// searchBox is the shared "/"-triggered live filter used by every list screen
// (fixtures, per-game ranking, admin enter-result and all-bets). Screens own the
// navigation keys; the box owns its text state and rendering.
type searchBox struct {
	input  textinput.Model
	active bool
}

func newSearchBox(placeholder string) searchBox {
	in := textinput.New()
	in.Placeholder = placeholder
	in.Prompt = "/ "
	in.PromptStyle = cursorOn
	in.Cursor.Style = cursorOn
	return searchBox{input: in}
}

// open activates the box and focuses it; returns the blink cmd for the caret.
func (s *searchBox) open() tea.Cmd {
	s.active = true
	s.input.Focus()
	return textinput.Blink
}

// close deactivates and clears the box.
func (s *searchBox) close() {
	s.active = false
	s.input.SetValue("")
	s.input.Blur()
}

// query returns the trimmed, lower-cased filter text.
func (s searchBox) query() string {
	return strings.TrimSpace(strings.ToLower(s.input.Value()))
}

// update feeds an edit keystroke to the text field.
func (s *searchBox) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return cmd
}

// view renders the box; empty when inactive and unused.
func (s searchBox) view() string {
	if !s.active && s.input.Value() == "" {
		return ""
	}
	return "  " + s.input.View() + "\n\n"
}

// matchHit reports whether a match matches the (already lower-cased) query over
// its team names and group label.
func matchHit(mt models.Match, q string) bool {
	if q == "" {
		return true
	}
	hay := strings.ToLower(mt.TeamA + " " + mt.TeamB + " " + mt.GroupLabel)
	return strings.Contains(hay, q)
}

// filterMatches keeps only the matches that hit the query (returns the input
// slice unchanged when the query is empty).
func filterMatches(in []models.Match, q string) []models.Match {
	if q == "" {
		return in
	}
	out := make([]models.Match, 0, len(in))
	for _, mt := range in {
		if matchHit(mt, q) {
			out = append(out, mt)
		}
	}
	return out
}
