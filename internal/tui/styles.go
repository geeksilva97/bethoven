package tui

import "github.com/charmbracelet/lipgloss"

// A small, calm palette. BEThoven leans classy (it's a Beethoven pun), so:
// gold accents on a dark terminal.
var (
	gold   = lipgloss.Color("#D4AF37")
	dim    = lipgloss.Color("241")
	green  = lipgloss.Color("42")
	red    = lipgloss.Color("203")
	subtle = lipgloss.Color("250")
	cyan   = lipgloss.Color("45") // "live" accent — distinct from gold/green/red

	titleStyle = lipgloss.NewStyle().Foreground(gold).Bold(true)
	helpStyle  = lipgloss.NewStyle().Foreground(dim)
	errStyle   = lipgloss.NewStyle().Foreground(red).Bold(true)
	okStyle    = lipgloss.NewStyle().Foreground(green)
	cursorOn   = lipgloss.NewStyle().Foreground(gold).Bold(true)
	labelStyle = lipgloss.NewStyle().Foreground(subtle)
	lockStyle  = lipgloss.NewStyle().Foreground(dim).Italic(true)

	// liveStyle marks anything live/provisional (in-play scores, partial
	// leaderboard points) so it's never mistaken for a settled value.
	liveStyle = lipgloss.NewStyle().Foreground(cyan).Bold(true)

	// selBar is the unmistakable "you are here" highlight: dark text on a solid
	// gold bar. Used for the selected row in lists and the focused form field —
	// a full-width background reads at a glance where a foreground tint doesn't.
	selBar = lipgloss.NewStyle().Foreground(lipgloss.Color("232")).Background(gold).Bold(true)
)

// liveLegend is the shared one-liner shown on screens that display live values.
const liveLegend = "⚡ live — provisional, from matches in play (not final)"
