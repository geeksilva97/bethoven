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

	// Podium accents for the player cards: gold (reuses the house gold), silver and
	// bronze. Only used to colour a top-three card's border + medal.
	silver = lipgloss.Color("#C0C0C0")
	bronze = lipgloss.Color("#CD7F32")

	titleStyle = lipgloss.NewStyle().Foreground(gold).Bold(true)
	helpStyle  = lipgloss.NewStyle().Foreground(dim)
	errStyle   = lipgloss.NewStyle().Foreground(red).Bold(true)
	okStyle    = lipgloss.NewStyle().Foreground(green)
	cursorOn   = lipgloss.NewStyle().Foreground(gold).Bold(true)
	labelStyle = lipgloss.NewStyle().Foreground(subtle)
	lockStyle  = lipgloss.NewStyle().Foreground(dim).Italic(true)

	// statVal makes a card metric's VALUE pop against its dim label — the numbers
	// were getting lost when the whole line was help-grey.
	statVal = lipgloss.NewStyle().Foreground(subtle).Bold(true)

	// drawStyle marks a draw in the recent-form strip. Help-grey (dim) is too
	// faint to read on a dark terminal, so use the brighter neutral, bold — it
	// stays clearly "neither win nor loss" without borrowing the red/green.
	drawStyle = lipgloss.NewStyle().Foreground(subtle).Bold(true)

	// liveStyle marks anything live/provisional (in-play scores, partial
	// leaderboard points) so it's never mistaken for a settled value.
	liveStyle = lipgloss.NewStyle().Foreground(cyan).Bold(true)

	// koOutStyle dims a knocked-out club in the bracket — a notch darker than the
	// help-grey skeleton (238 vs 241) so an eliminated team reads as "out" without
	// vanishing entirely. Used for losers of a finished tie and teams that didn't
	// reach the furthest drawn round.
	koOutStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))

	// bracketPathStyle lights the bracket-tree path of the team a player is
	// tracing (leaf name + connector skeleton up to the Champion box). Gold bold
	// is the existing "focus / you are here" accent and isn't used elsewhere in
	// the tree, so it reads clearly against the grey label/skeleton.
	bracketPathStyle = lipgloss.NewStyle().Foreground(gold).Bold(true)

	// commentStyle renders BETanIA's leaderboard commentary. It deliberately sets
	// NO foreground colour so it uses the terminal's default text colour — readable
	// on both light and dark themes (any fixed grey is unreadable on one of them).
	// Italic + the 🤖 prefix + indentation set it apart without relying on colour.
	commentStyle = lipgloss.NewStyle().Italic(true)
	// botMark accents the 🤖 label so the commentary is identifiable; cyan reads on
	// both backgrounds (it's the same accent live scores use).
	botMark = lipgloss.NewStyle().Foreground(cyan).Bold(true)

	// weightStyle accents a round-weight multiplier chip (e.g. "×2") so the raised
	// stakes of a knockout round pop where a player places the pick. Gold bold is the
	// house "this matters" accent, matching the selected-row bar it sits inside.
	weightStyle = lipgloss.NewStyle().Foreground(gold).Bold(true)

	// selBar is the unmistakable "you are here" highlight: dark text on a solid
	// gold bar. Used for the selected row in lists and the focused form field —
	// a full-width background reads at a glance where a foreground tint doesn't.
	selBar = lipgloss.NewStyle().Foreground(lipgloss.Color("232")).Background(gold).Bold(true)
)

// liveLegend is the shared one-liner shown on screens that display live values.
const liveLegend = "⚡ live — provisional, from matches in play (not final)"
