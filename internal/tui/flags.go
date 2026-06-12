package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// teamFlags maps a team name (exactly as spelled in fixtures.json) to its flag
// emoji. Keys must match the stored TeamA/TeamB strings byte-for-byte. Adding a
// team — e.g. a knockout qualifier entered via the admin TUI — is one line here.
// Home nations use their proper Unicode subdivision (tag-sequence) flags.
var teamFlags = map[string]string{
	"Mexico":               "🇲🇽",
	"South Africa":         "🇿🇦",
	"South Korea":          "🇰🇷",
	"Czech Republic":       "🇨🇿",
	"Canada":               "🇨🇦",
	"Bosnia & Herzegovina": "🇧🇦",
	"Qatar":                "🇶🇦",
	"Switzerland":          "🇨🇭",
	"Brazil":               "🇧🇷",
	"Morocco":              "🇲🇦",
	"Haiti":                "🇭🇹",
	"Scotland":             "🏴󠁧󠁢󠁳󠁣󠁴󠁿",
	"USA":                  "🇺🇸",
	"Paraguay":             "🇵🇾",
	"Australia":            "🇦🇺",
	"Turkey":               "🇹🇷",
	"Germany":              "🇩🇪",
	"Curaçao":              "🇨🇼",
	"Ivory Coast":          "🇨🇮",
	"Ecuador":              "🇪🇨",
	"Netherlands":          "🇳🇱",
	"Japan":                "🇯🇵",
	"Sweden":               "🇸🇪",
	"Tunisia":              "🇹🇳",
	"Belgium":              "🇧🇪",
	"Egypt":                "🇪🇬",
	"Iran":                 "🇮🇷",
	"New Zealand":          "🇳🇿",
	"Spain":                "🇪🇸",
	"Cape Verde":           "🇨🇻",
	"Saudi Arabia":         "🇸🇦",
	"Uruguay":              "🇺🇾",
	"France":               "🇫🇷",
	"Senegal":              "🇸🇳",
	"Iraq":                 "🇮🇶",
	"Norway":               "🇳🇴",
	"Argentina":            "🇦🇷",
	"Algeria":              "🇩🇿",
	"Austria":              "🇦🇹",
	"Jordan":               "🇯🇴",
	"Portugal":             "🇵🇹",
	"DR Congo":             "🇨🇩",
	"Uzbekistan":           "🇺🇿",
	"Colombia":             "🇨🇴",
	"England":              "🏴󠁧󠁢󠁥󠁮󠁧󠁿",
	"Croatia":              "🇭🇷",
	"Ghana":                "🇬🇭",
	"Panama":               "🇵🇦",
	"Wales":                "🏴󠁧󠁢󠁷󠁬󠁳󠁿",
}

// flagFallback stands in for any team not in the map (placeholder knockout
// slots, test fixtures, typos). A neutral white flag always renders.
const flagFallback = "🏳️"

// flagFor returns a team's flag emoji, or the neutral fallback if unknown.
func flagFor(team string) string {
	if f, ok := teamFlags[team]; ok {
		return f
	}
	return flagFallback
}

// withFlag prefixes a team name with its flag, e.g. "🇧🇷 Brazil". Use this at
// every variable-width render site (titles, score-field labels).
func withFlag(team string) string {
	return flagFor(team) + " " + team
}

// teamCell renders withFlag(team) padded — or trimmed — to exactly width
// *display columns*, so fixed-width table layouts stay aligned despite the
// double-width flag emoji. lipgloss.Width measures real cell width (rune-count
// formats like %-14s can't). Trimming only ever eats trailing name text since
// the flag sits at the front, so it never splits an emoji's runes.
func teamCell(team string, width int) string {
	s := withFlag(team)
	if w := lipgloss.Width(s); w > width {
		// Drop trailing runes until it fits.
		r := []rune(s)
		for len(r) > 0 && lipgloss.Width(string(r)) > width {
			r = r[:len(r)-1]
		}
		return string(r)
	} else if w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}
