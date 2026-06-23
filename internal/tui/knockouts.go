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
	case "tab", "left", "right", "h", "l":
		if m.koView == koViewGroups {
			m.koView = koViewBracket
		} else {
			m.koView = koViewGroups
		}
		m.koScroll = 0
		return m, nil
	case "down", "j":
		if m.koView == koViewBracket {
			m.koScroll++
		}
		return m, nil
	case "up", "k":
		if m.koView == koViewBracket && m.koScroll > 0 {
			m.koScroll--
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
	help := "tab: " + map[int]string{koViewGroups: "bracket", koViewBracket: "qualification"}[m.koView]
	if m.koView == koViewBracket {
		help += " · ↑/↓: scroll"
	}
	help += " · esc: back · q: quit"
	body += "\n" + statusLine(m) + helpStyle.Render(help)
	// Breathing room: a top blank line and a left margin on the whole screen.
	return lipgloss.NewStyle().Margin(1, 0, 0, 2).Render(body)
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
	out += arrangeColumns(blocks, m.width, koBlockWidth)
	out += " " + okStyle.Render("green = advancing") + helpStyle.Render("   ") +
		liveStyle.Render("cyan = 3rd place") + helpStyle.Render("   ") +
		helpStyle.Render("dim = eliminated") + "\n\n"

	out += titleStyle.Render("Best 3rd-placed (top 8 advance)") + "\n"
	out += renderThirdRace(m.ko.ThirdPlace)
	return out
}

const koBlockWidth = 30 // group-table column width incl. padding (room for the qualification tag)

// renderGroupBlock renders one group's table as a fixed-width block.
func renderGroupBlock(g standings.Group) string {
	var b strings.Builder
	b.WriteString(labelStyle.Render(g.Label) + "\n")
	for _, r := range g.Rows {
		b.WriteString(groupRow(r) + "\n")
	}
	return b.String()
}

// groupRow renders one team line: rank, name, played, signed GD, points. Status
// is conveyed by colour only — green = advancing (top two), cyan = third place
// (in the best-thirds race), dim = eliminated.
func groupRow(r standings.TeamRow) string {
	name := truncate(r.Team, 13)
	line := fmt.Sprintf("%d %-13s %d %+3d %2d", r.Rank, name, r.Played, r.GD(), r.Pts)
	switch {
	case r.Rank <= 2:
		return okStyle.Render(line)
	case r.Rank == 3:
		return liveStyle.Render(line)
	default:
		return helpStyle.Render(line)
	}
}

// renderThirdRace renders the third-placed teams ranked across groups, with the
// qualification cut-line drawn after the eighth. Colour-only status: green =
// advancing, gold = tied (pending official tiebreak), dim = below the cut.
func renderThirdRace(thirds []standings.ThirdPlace) string {
	if len(thirds) == 0 {
		return helpStyle.Render("  (no third-placed teams yet)") + "\n"
	}
	var b strings.Builder
	for i, tp := range thirds {
		name := truncate(tp.Team, 16)
		line := fmt.Sprintf(" %2d %-16s (%s) %d  %+3d  %2d", tp.Rank, name, groupLetter(tp.Group), tp.Played, tp.GD(), tp.Pts)
		style := helpStyle
		if tp.Qualifies {
			style = okStyle
		}
		if tp.Tied { // gold overrides: the spot is provisional
			style = titleStyle
		}
		b.WriteString(style.Render(line) + "\n")
		if i+1 == standings.QualifyingThirds && i+1 < len(thirds) {
			b.WriteString(helpStyle.Render(" ── qualification cut ──────────────") + "\n")
		}
	}
	b.WriteString("\n " + okStyle.Render("green = advancing") + helpStyle.Render("   ") +
		titleStyle.Render("gold = tied, pending official tiebreak") + "\n")
	return b.String()
}

// viewKnockoutBracket renders the knockout ladder. When the admin has not entered
// real knockout matchups yet, it draws the full projected bracket tree (R32 →
// Final) so a team's path through the rounds is visible; once matches are entered
// it falls back to listing each round's ties.
func (m Model) viewKnockoutBracket() string {
	projecting := len(m.ko.Projected) > 0 && !bracketDrawn(m.ko)
	if projecting {
		return m.viewBracketTree()
	}
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

// viewBracketTree draws the projected R32→Final bracket as a scrollable tree.
func (m Model) viewBracketTree() string {
	lines := bracketLines(standings.BracketLeaves(m.ko.Projected))

	out := titleStyle.Render("Knockouts — bracket") +
		helpStyle.Render("  projected, if the group stage ended now") + "\n\n"

	cap := m.height - 8 // title, top margin, section header, markers, status, help
	if cap < 6 {
		cap = 6
	}
	off := m.koScroll
	if off > len(lines)-cap {
		off = len(lines) - cap
	}
	if off < 0 {
		off = 0
	}
	end := off + cap
	if end > len(lines) {
		end = len(lines)
	}

	if off > 0 {
		out += helpStyle.Render(fmt.Sprintf("  ↑ %d more", off)) + "\n"
	} else {
		out += "\n"
	}
	out += strings.Join(lines[off:end], "\n") + "\n"
	if end < len(lines) {
		out += helpStyle.Render(fmt.Sprintf("  ↓ %d more", len(lines)-end)) + "\n"
	}
	return out
}

// bracketLines renders the 16-leaf balanced bracket (R32→Final) as fixed-width
// lines. Only the Round-of-32 teams are known under a projection, so the inner
// rounds are drawn as the connector skeleton — the path each team would follow.
// leaves must be in bracket position order (see standings.BracketLeaves).
func bracketLines(leaves []standings.ProjMatch) []string {
	if len(leaves) != 16 {
		return []string{helpStyle.Render("  bracket unavailable (incomplete projection)")}
	}
	const (
		nameW   = 16
		segW    = 6
		nMerges = 5
		height  = 63 // 32 leaves at rows 0,2,…,62
	)
	width := nameW + nMerges*segW + 12

	grid := make([][]rune, height)
	for r := range grid {
		grid[r] = make([]rune, width)
		for c := range grid[r] {
			grid[r][c] = ' '
		}
	}
	put := func(r, c int, s string) {
		for _, ch := range s {
			if r >= 0 && r < height && c >= 0 && c < width {
				grid[r][c] = ch
			}
			c++
		}
	}
	rowLevel := func(lvl, j int) int { return (1 << lvl) - 1 + j*(1 << (lvl + 1)) }
	xv := func(lvl int) int { return nameW + lvl*segW + 1 }

	// Level-0 leaves: the two teams of each R32 tie.
	teams := make([]string, 0, 32)
	for _, lf := range leaves {
		h, a := lf.HomeTeam, lf.AwayTeam
		if h == "" {
			h = lf.HomeDesc
		}
		if a == "" {
			a = lf.AwayDesc
		}
		teams = append(teams, h, a)
	}
	for i, name := range teams {
		put(rowLevel(0, i), 0, truncate(name, nameW-1))
	}

	for mlvl := 0; mlvl < nMerges; mlvl++ {
		parents := (32 >> mlvl) / 2
		v := xv(mlvl)
		for j := 0; j < parents; j++ {
			r0 := rowLevel(mlvl, 2*j)
			r1 := rowLevel(mlvl, 2*j+1)
			pr := rowLevel(mlvl+1, j)
			if mlvl == 0 { // connect the team names to the first corners
				for x := nameW; x < v; x++ {
					grid[r0][x] = '─'
					grid[r1][x] = '─'
				}
			}
			grid[r0][v] = '┐'
			grid[r1][v] = '┘'
			for r := r0 + 1; r < r1; r++ {
				if grid[r][v] == ' ' {
					grid[r][v] = '│'
				}
			}
			grid[pr][v] = '├'
			end := xv(mlvl + 1) // next merge's corner column
			if mlvl == nMerges-1 {
				end = v + segW
			}
			for x := v + 1; x < end && x < width; x++ {
				if grid[pr][x] == ' ' {
					grid[pr][x] = '─'
				}
			}
		}
	}
	put(rowLevel(nMerges, 0), xv(nMerges-1)+segW, "Champion")

	// Header row: which round each vertical column decides.
	hdr := make([]rune, width)
	for c := range hdr {
		hdr[c] = ' '
	}
	for _, h := range []struct {
		c int
		s string
	}{
		{0, "ROUND OF 32"}, {xv(0) - 1, "R32"}, {xv(1) - 1, "R16"},
		{xv(2) - 1, "QF"}, {xv(3) - 1, "SF"}, {xv(4) - 1, "FINAL"},
	} {
		for i, ch := range h.s {
			if h.c+i < width {
				hdr[h.c+i] = ch
			}
		}
	}

	// Style: team names bright, connector skeleton dim.
	out := make([]string, 0, height+1)
	out = append(out, helpStyle.Render(string(hdr)))
	for r := 0; r < height; r++ {
		left := string(grid[r][:nameW])
		right := string(grid[r][nameW:])
		out = append(out, labelStyle.Render(left)+helpStyle.Render(right))
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

