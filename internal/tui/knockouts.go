package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/models"
	"bethoven/internal/service"
	"bethoven/internal/standings"
)

// knockouts screen view modes.
const (
	koViewGroups  = iota // group tables + best-third-placed race (qualification)
	koViewBracket        // the knockout ladder (Round of 32 → Final)
)

// bracketDrawn reports whether any knockout match has been entered yet, so the
// screen can open straight to the bracket once the knockouts begin.
func bracketDrawn(pic service.KnockoutPicture) bool {
	for _, rd := range pic.Bracket {
		if len(rd.Matches) > 0 {
			return true
		}
	}
	return false
}

// updateKnockouts handles the read-only Knockouts screen: tab/←/→ toggle between
// the qualification picture and the bracket, esc returns to the menu, q quits.
func (m Model) updateKnockouts(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "esc", "enter":
		return m.goMenu(), nil
	case "tab", "left", "right", "h", "l", " ":
		if m.koView == koViewGroups {
			m.koView = koViewBracket
		} else {
			m.koView = koViewGroups
		}
		return m, nil
	}
	return m, nil
}

func (m Model) viewKnockouts() string {
	var body string
	switch m.koView {
	case koViewBracket:
		body = m.viewKnockoutBracket()
	default:
		body = m.viewKnockoutGroups()
	}
	help := "tab: " + map[int]string{koViewGroups: "bracket", koViewBracket: "qualification"}[m.koView] +
		" · esc: back · q: quit"
	return body + "\n" + statusLine(m) + helpStyle.Render(help)
}

// viewKnockoutGroups renders every group's table plus the cross-group race for
// the eight best third-placed spots.
func (m Model) viewKnockoutGroups() string {
	out := titleStyle.Render("Knockouts — who's going through") + "\n"
	out += helpStyle.Render("Top 2 of each group + the 8 best 3rd-placed teams advance. In-play scores included.") + "\n\n"

	// Group tables, arranged in as many columns as the terminal allows.
	blocks := make([]string, 0, len(m.ko.Groups))
	for _, g := range m.ko.Groups {
		blocks = append(blocks, renderGroupBlock(g))
	}
	out += arrangeColumns(blocks, m.width, koBlockWidth) + "\n"

	out += titleStyle.Render("Best 3rd-placed (top 8 advance)") + "\n"
	out += renderThirdRace(m.ko.ThirdPlace)
	return out
}

const koBlockWidth = 26 // group-table column width incl. padding

// renderGroupBlock renders one group's table as a fixed-width block.
func renderGroupBlock(g standings.Group) string {
	var b strings.Builder
	b.WriteString(labelStyle.Render(g.Label) + "\n")
	for _, r := range g.Rows {
		b.WriteString(groupRow(r) + "\n")
	}
	return b.String()
}

// groupRow renders one team line: rank, name, played, signed GD, points, and a
// qualification marker (✓ top two, · third place, ? unresolved tie).
func groupRow(r standings.TeamRow) string {
	name := truncate(r.Team, 13)
	line := fmt.Sprintf("%d %-13s %d %+3d %2d", r.Rank, name, r.Played, r.GD(), r.Pts)
	tag := "  "
	switch {
	case r.Rank <= 2:
		tag = okStyle.Render(" ✓")
	case r.Rank == 3:
		tag = liveStyle.Render(" ·")
	}
	if r.Tied {
		tag += errStyle.Render("?")
	}
	style := labelStyle
	if r.Rank <= 2 {
		style = okStyle
	} else if r.Rank > 3 {
		style = helpStyle
	}
	return style.Render(line) + tag
}

// renderThirdRace renders the third-placed teams ranked across groups, with the
// qualification cut-line drawn after the eighth.
func renderThirdRace(thirds []standings.ThirdPlace) string {
	if len(thirds) == 0 {
		return helpStyle.Render("  (no third-placed teams yet)") + "\n"
	}
	var b strings.Builder
	for i, tp := range thirds {
		name := truncate(tp.Team, 16)
		line := fmt.Sprintf(" %2d %-16s (%s) %d  %+3d  %2d", tp.Rank, name, groupLetter(tp.Group), tp.Played, tp.GD(), tp.Pts)
		style := helpStyle
		mark := ""
		if tp.Qualifies {
			style = okStyle
			mark = okStyle.Render(" ✓")
		}
		if tp.Tied {
			mark += errStyle.Render(" ?")
		}
		b.WriteString(style.Render(line) + mark + "\n")
		if i+1 == standings.QualifyingThirds && i+1 < len(thirds) {
			b.WriteString(helpStyle.Render(" ── qualification cut ──────────────") + "\n")
		}
	}
	if anyTied(thirds) {
		b.WriteString(helpStyle.Render(" ? = tied — pending official tiebreak (fair play, FIFA ranking)") + "\n")
	}
	return b.String()
}

// viewKnockoutBracket renders the knockout ladder, one round per section, with
// "not drawn yet" for rounds the admin has not added matches to.
func (m Model) viewKnockoutBracket() string {
	out := titleStyle.Render("Knockouts — bracket") + "\n\n"
	for _, rd := range m.ko.Bracket {
		out += labelStyle.Render(rd.Label) + "\n"
		if len(rd.Matches) == 0 {
			out += helpStyle.Render("  (not drawn yet)") + "\n\n"
			continue
		}
		for _, mt := range rd.Matches {
			out += "  " + koMatchLine(mt) + "\n"
		}
		out += "\n"
	}
	return out
}

// koMatchLine renders one bracket match: the final score if played, the live
// score if in play, otherwise the kickoff time.
func koMatchLine(mt models.Match) string {
	switch {
	case mt.Finished && mt.ScoreA != nil:
		return fmt.Sprintf("%s %s %s", truncate(mt.TeamA, 16), okStyle.Render(fmtResult(mt)), truncate(mt.TeamB, 16))
	case mt.Live:
		return fmt.Sprintf("%s %s %s", truncate(mt.TeamA, 16), liveScore(mt), truncate(mt.TeamB, 16))
	default:
		return labelStyle.Render(fmt.Sprintf("%s v %s", truncate(mt.TeamA, 16), truncate(mt.TeamB, 16))) +
			helpStyle.Render("  "+fmtKickoff(mt.StartsAt))
	}
}

// arrangeColumns lays blocks out left-to-right in as many fixed-width columns as
// fit the terminal, wrapping to new rows. A zero/narrow width falls back to one
// column (e.g. in tests).
func arrangeColumns(blocks []string, width, blockW int) string {
	cols := 1
	if width > blockW {
		cols = width / blockW
	}
	if cols < 1 {
		cols = 1
	}
	cell := lipgloss.NewStyle().Width(blockW)
	var out strings.Builder
	for i := 0; i < len(blocks); i += cols {
		end := i + cols
		if end > len(blocks) {
			end = len(blocks)
		}
		row := make([]string, 0, end-i)
		for _, blk := range blocks[i:end] {
			row = append(row, cell.Render(blk))
		}
		out.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, row...) + "\n")
	}
	return out.String()
}

// groupLetter extracts the short group label ("A" from "Group A") for compact
// display; falls back to the full label.
func groupLetter(label string) string {
	if f := strings.Fields(label); len(f) > 0 {
		return f[len(f)-1]
	}
	return label
}

func anyTied(thirds []standings.ThirdPlace) bool {
	for _, tp := range thirds {
		if tp.Tied {
			return true
		}
	}
	return false
}
