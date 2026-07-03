package tui

import (
	"fmt"
	"strings"
	"unicode"

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

// treeShown reports whether the projected R32→Final tree can be drawn (a full
// 16-leaf projection exists). The tree persists once knockouts begin — entered
// matches are overlaid onto it rather than collapsing it to a flat list.
func treeShown(pic service.KnockoutPicture) bool {
	return len(standings.BracketLeaves(pic.Projected)) == 16
}

// laterRoundMatches returns the entered ties for the rounds above R32 (R16, QF,
// SF, Final) in ladder order, so bracketLines can advance winners forward. R32 is
// excluded — it's the leaf layer, fed in separately via the projection.
func laterRoundMatches(pic service.KnockoutPicture) [][]models.Match {
	out := make([][]models.Match, 0, len(pic.Bracket))
	for _, rd := range pic.Bracket {
		if rd.Phase == models.PhaseRound32 {
			continue
		}
		out = append(out, rd.Matches)
	}
	return out
}

// r32Entered returns the entered Round-of-32 matches from the bracket.
func r32Entered(pic service.KnockoutPicture) []models.Match {
	for _, rd := range pic.Bracket {
		if rd.Phase == models.PhaseRound32 {
			return rd.Matches
		}
	}
	return nil
}

// overlayEnteredR32 substitutes entered R32 matches into their projected slots,
// returning the overlaid projection plus a match-number → entered-match map for
// score display. Entered matches are matched to projected ties first by exact
// team pair, then by a single determined (non-third) anchor side — so an
// officially-drawn winner-vs-third tie lands on the right slot even when our
// projected third differs from FIFA's. Advancement is never inferred: only R32
// (which comes straight from the group tables) is overlaid.
func overlayEnteredR32(projected []standings.ProjMatch, entered []models.Match) ([]standings.ProjMatch, map[int]models.Match) {
	out := make([]standings.ProjMatch, len(projected))
	copy(out, projected)
	byNum := map[int]models.Match{} // match number -> entered match
	used := make([]bool, len(entered))
	pairKey := func(a, b string) string {
		if a > b {
			a, b = b, a
		}
		return a + "\x00" + b
	}
	// Pass 1: exact team pair.
	for ei, e := range entered {
		ek := pairKey(e.TeamA, e.TeamB)
		for i := range out {
			p := out[i]
			if p.HomeTeam == "" || p.AwayTeam == "" {
				continue
			}
			if _, taken := byNum[p.Match]; taken {
				continue
			}
			if pairKey(p.HomeTeam, p.AwayTeam) == ek {
				byNum[p.Match] = e
				used[ei] = true
				break
			}
		}
	}
	// Pass 2: a single determined non-third anchor side (places drawn
	// winner-vs-third ties whose projected third we may have guessed differently).
	isThird := func(desc string) bool { return strings.HasPrefix(desc, "3rd") }
	for ei, e := range entered {
		if used[ei] {
			continue
		}
		cand := -1
		for i := range out {
			p := out[i]
			if _, taken := byNum[p.Match]; taken {
				continue
			}
			hit := (!isThird(p.HomeDesc) && p.HomeTeam != "" && (p.HomeTeam == e.TeamA || p.HomeTeam == e.TeamB)) ||
				(!isThird(p.AwayDesc) && p.AwayTeam != "" && (p.AwayTeam == e.TeamA || p.AwayTeam == e.TeamB))
			if hit {
				if cand != -1 {
					cand = -2 // ambiguous — leave it for the team-pair pass only
					break
				}
				cand = i
			}
		}
		if cand >= 0 {
			byNum[out[cand].Match] = e
			used[ei] = true
		}
	}
	// Substitute the real teams (admin's home/away order) into their slots.
	for i := range out {
		if e, ok := byNum[out[i].Match]; ok {
			out[i].HomeTeam, out[i].AwayTeam = e.TeamA, e.TeamB
		}
	}
	// An entered tie places its teams authoritatively. The overlay's anchor pass
	// can land a team on a slot OTHER than the one the projection guessed for it, so
	// any non-entered slot still showing a now-claimed team is a stale guess — reset
	// it to its descriptor, otherwise that team appears twice in the tree.
	claimed := map[string]bool{}
	for _, e := range byNum {
		claimed[e.TeamA] = true
		claimed[e.TeamB] = true
	}
	for i := range out {
		if _, entered := byNum[out[i].Match]; entered {
			continue // this slot IS the authoritative source — leave it
		}
		if out[i].HomeTeam != "" && claimed[out[i].HomeTeam] {
			out[i].HomeTeam = ""
		}
		if out[i].AwayTeam != "" && claimed[out[i].AwayTeam] {
			out[i].AwayTeam = ""
		}
	}
	return out, byNum
}

// updateKnockouts handles the read-only Knockouts screen: tab toggles between the
// qualification picture and the bracket; in the projected bracket tree ←/→ cycle
// the team whose path is lit; ↑/↓ scroll; esc/b return to the menu, q quits.
func (m Model) updateKnockouts(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	// ←/→ trace a team only on the bracket tree; elsewhere they toggle the view.
	tracing := m.koView == koViewBracket && treeShown(m.ko)
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "esc", "enter", "b":
		return m.goMenu(), nil
	case "tab":
		m.koView, m.koScroll, m.koTraceIdx = toggleKoView(m.koView), 0, -1
		return m, nil
	case "right", "l":
		if tracing {
			return m.cycleTrace(1), nil
		}
		m.koView, m.koScroll, m.koTraceIdx = toggleKoView(m.koView), 0, -1
		return m, nil
	case "left", "h":
		if tracing {
			return m.cycleTrace(-1), nil
		}
		m.koView, m.koScroll, m.koTraceIdx = toggleKoView(m.koView), 0, -1
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

// toggleKoView flips between the qualification and bracket views.
func toggleKoView(v int) int {
	if v == koViewGroups {
		return koViewBracket
	}
	return koViewGroups
}

// cycleTrace steps the lit team by dir (±1), wrapping off → team0 → … → last →
// off, and scrolls so the newly lit leaf stays on screen.
func (m Model) cycleTrace(dir int) Model {
	n := len(koTeamNames(standings.BracketLeaves(m.ko.Projected)))
	if n == 0 {
		return m
	}
	m.koTraceIdx += dir
	if m.koTraceIdx >= n {
		m.koTraceIdx = -1
	} else if m.koTraceIdx < -1 {
		m.koTraceIdx = n - 1
	}
	if m.koTraceIdx >= 0 {
		cap := m.height - 9 - lineCount(m.viewEnteredLaterRounds()) // match viewBracketTree's window
		if cap < 6 {
			cap = 6
		}
		// The leaf sits at output line 1+2*idx (header offset); centre it. The
		// view re-clamps koScroll to range, so an approximate target is fine.
		m.koScroll = 1 + 2*m.koTraceIdx - cap/2
		if m.koScroll < 0 {
			m.koScroll = 0
		}
	}
	return m
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
		if treeShown(m.ko) {
			help += " · ←/→: trace team"
		}
		help += " · ↑/↓: scroll"
	}
	help += " · esc/b: back · q: quit"
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

// viewKnockoutBracket renders the knockout ladder as the full R32→Final tree
// (so a team's path through the rounds is always visible), with entered matches
// overlaid onto it. It only falls back to a flat list of entered ties when no
// projection exists yet (too early to draw a tree).
func (m Model) viewKnockoutBracket() string {
	if treeShown(m.ko) {
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
			out += "  " + koMatchLine(mt, m.ko.Eliminated) + "\n"
		}
		out += "\n"
	}
	return out
}

// viewBracketTree draws the R32→Final bracket as a scrollable tree. The R32
// leaves come from the group-table projection, with any entered R32 matches
// overlaid (real teams + score, plus the shootout total when drawn at 90'). The
// tree still doesn't auto-advance winners into later rounds — entered R16+ ties
// are listed beneath it — but a settled R32 tie now dims its loser (incl. the
// penalty loser once the shootout is recorded), so who went through is visible.
func (m Model) viewBracketTree() string {
	overlaid, byNum := overlayEnteredR32(m.ko.Projected, r32Entered(m.ko))
	leaves := standings.BracketLeaves(overlaid)
	scores := bracketScores(leaves, byNum)
	lines := bracketLines(bracketInput{
		leaves: leaves,
		trace:  m.koTraceIdx,
		scores: scores,
		elim:   m.ko.Eliminated,
		r32:    byNum,
		later:  laterRoundMatches(m.ko),
	})

	sub := "  projected, if the group stage ended now"
	switch {
	case len(byNum) >= len(leaves) && len(leaves) > 0:
		sub = "" // fully drawn — nothing left to project
	case len(byNum) > 0:
		sub = "  drawn ties shown; the rest projected"
	}
	out := titleStyle.Render("Knockouts — bracket") + helpStyle.Render(sub) + "\n"
	names := koTeamNames(leaves)
	if m.koTraceIdx >= 0 && m.koTraceIdx < len(names) {
		out += bracketPathStyle.Render("Tracing: "+names[m.koTraceIdx]) +
			helpStyle.Render("  ←/→ change · tab clears") + "\n\n"
	} else {
		out += helpStyle.Render("←/→ trace a team's path to the final") + "\n\n"
	}

	// The entered R16→Final ties are listed BELOW the tree, outside the scroll
	// window — so their height must come out of the tree's budget too, otherwise the
	// combined frame overflows the terminal and clips the top of the tree.
	later := m.viewEnteredLaterRounds()
	cap := m.height - 9 - lineCount(later) // title, top margin, trace hint, markers, status, help + the games list below
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
	out += later
	return out
}

// viewEnteredLaterRounds lists entered R16→Final ties beneath the tree. The tree
// can't place them (advancement is never inferred — a 90' score can't reveal a
// shootout winner), so they're shown as a compact list; empty rounds are omitted.
func (m Model) viewEnteredLaterRounds() string {
	var out string
	for _, rd := range m.ko.Bracket {
		if rd.Phase == models.PhaseRound32 || len(rd.Matches) == 0 {
			continue
		}
		out += "\n" + labelStyle.Render(rd.Label) + "\n"
		for _, mt := range rd.Matches {
			out += "  " + koMatchLine(mt, m.ko.Eliminated) + "\n"
		}
	}
	return out
}

// bracketScores maps a team's leaf-row index (home=2i, away=2i+1 for leaf i) to
// a short goals suffix, for entered R32 matches that are finished or in play.
// Drawn-but-unplayed ties get no suffix (just the matchup).
func bracketScores(leaves []standings.ProjMatch, byNum map[int]models.Match) map[int]string {
	scores := map[int]string{}
	for i, lf := range leaves {
		mt, ok := byNum[lf.Match]
		if !ok {
			continue
		}
		switch {
		case mt.Finished && mt.ScoreA != nil && mt.ScoreB != nil:
			// A penalty shootout (drawn at 90') appends the shootout total in
			// parens, e.g. "1 (4)" / "1 (2)", so the winner is visible in the tree.
			if mt.PenA != nil && mt.PenB != nil {
				scores[2*i] = fmt.Sprintf("%d (%d)", *mt.ScoreA, *mt.PenA)
				scores[2*i+1] = fmt.Sprintf("%d (%d)", *mt.ScoreB, *mt.PenB)
			} else {
				scores[2*i] = fmt.Sprintf("%d", *mt.ScoreA)
				scores[2*i+1] = fmt.Sprintf("%d", *mt.ScoreB)
			}
		case mt.Live:
			scores[2*i] = fmt.Sprintf("%d", mt.LiveScoreA)
			scores[2*i+1] = fmt.Sprintf("%d", mt.LiveScoreB)
		}
	}
	return scores
}

// bracketInput is everything bracketLines needs to draw the tree.
type bracketInput struct {
	leaves []standings.ProjMatch  // the 16 R32 ties in bracket position order
	trace  int                    // team index whose path to light, or <0 for none
	scores map[int]string         // teamIdx -> goals suffix for a played R32 leaf
	elim   map[string]bool        // clubs that are out (dimmed)
	r32    map[int]models.Match   // R32 match-number -> entered match (winner source)
	later  [][]models.Match       // entered R16,QF,SF,Final ties (ladder order)
}

// flagOverlay is a winner token (flag emoji or abbreviation) spliced onto a row
// at col, occupying width display columns — see styleBracketRow.
type flagOverlay struct {
	col, width int
	text       string // already styled, ready to print
}

// bracketLines renders the 16-leaf balanced bracket (R32→Final) as fixed-width
// lines. The Round-of-32 teams are the known leaves (projected, with entered
// matches overlaid); later rounds are filled in as each tie settles — the winner
// of a decided tie advances into the next column as a flag (or a short
// abbreviation when it has no flag). Undecided ties leave the connector skeleton
// bare. leaves must be in bracket position order (see standings.BracketLeaves);
// scores[teamIdx] adds a goals suffix to a leaf when its tie has been played.
// When trace is in 0..31 the leaf at that team index and the connector cells
// carrying its slot up to the Champion box are lit in the traced-path accent.
func bracketLines(in bracketInput) []string {
	leaves, trace, scores, elim := in.leaves, in.trace, in.scores, in.elim
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
	lit := make([][]bool, height)
	for r := range grid {
		grid[r] = make([]rune, width)
		lit[r] = make([]bool, width)
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

	// Level-0 leaves: the two teams of each R32 tie, with a goals suffix once played.
	teams := koTeamNames(leaves)
	elimRow := make([]bool, height) // leaf rows whose club has been knocked out
	for i, name := range teams {
		label := truncate(name, nameW-1)
		if sc := scores[i]; sc != "" {
			label = truncate(name, nameW-2-len(sc)) + " " + sc
		}
		r := rowLevel(0, i)
		put(r, 0, label)
		if elim[name] {
			elimRow[r] = true
		}
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

	// Fill winners forward, round by round. node[lvl][j] is the club that reached
	// that node ("" if undecided). Level 0 is the R32 leaves; each merge resolves a
	// tie into its parent: the R32 leaf tie via the entered R32 match, later rounds
	// via the entered tie pairing the two known children. Advancement is never
	// inferred — a tie only fills its parent once KOResult decides it (incl. on
	// penalties). The traced path doesn't depend on this; it lights the slot a team
	// WOULD follow regardless of results.
	node := make([][]string, nMerges+1)
	node[0] = teams
	for mlvl := 0; mlvl < nMerges; mlvl++ {
		parents := (32 >> mlvl) / 2
		node[mlvl+1] = make([]string, parents)
		for j := 0; j < parents; j++ {
			var mt *models.Match
			if mlvl == 0 {
				if m, ok := in.r32[leaves[j].Match]; ok {
					mt = &m
				}
			} else if ca, cb := node[mlvl][2*j], node[mlvl][2*j+1]; ca != "" && cb != "" && mlvl-1 < len(in.later) {
				mt = findKOTie(in.later[mlvl-1], ca, cb)
			}
			if mt != nil {
				if w, _, decided := service.KOResult(*mt); decided {
					node[mlvl+1][j] = w
				}
			}
		}
	}
	// Light the traced team's path, mirroring the draw loop above: its leaf name,
	// then at each merge level its corner, the vertical down to the junction, and
	// the line carrying the winner rightward — up to the Champion box.
	mark := func(r, c int) {
		if r >= 0 && r < height && c >= 0 && c < width {
			lit[r][c] = true
		}
	}
	if trace >= 0 && trace < len(teams) {
		nr := rowLevel(0, trace)
		for c := 0; c < nameW; c++ {
			mark(nr, c)
		}
		for mlvl := 0; mlvl < nMerges; mlvl++ {
			child := trace >> mlvl
			j := child / 2
			v := xv(mlvl)
			myRow := rowLevel(mlvl, child)
			pr := rowLevel(mlvl+1, j)
			if mlvl == 0 { // dashes from the team name to its first corner
				for c := nameW; c <= v; c++ {
					mark(myRow, c)
				}
			}
			lo, hi := myRow, pr // corner → junction vertical (inclusive)
			if lo > hi {
				lo, hi = hi, lo
			}
			for r := lo; r <= hi; r++ {
				mark(r, v)
			}
			end := xv(mlvl + 1)
			if mlvl == nMerges-1 {
				end = v + segW
			}
			for c := v + 1; c < end; c++ { // winner line toward the next round
				mark(pr, c)
			}
		}
		cr := rowLevel(nMerges, 0) // the Champion box
		for c := xv(nMerges-1) + segW; c < width; c++ {
			mark(cr, c)
		}
	}

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

	// Place each decided winner as a flag on its node row, just past the corner
	// that produced it (overwriting two connector cells — see styleBracketRow).
	// Done after lighting so a flag on the traced path picks up the accent.
	overlayRow := make([]flagOverlay, height)
	for lvl := 1; lvl <= nMerges; lvl++ {
		for j, team := range node[lvl] {
			if team == "" {
				continue
			}
			r, c := rowLevel(lvl, j), xv(lvl-1)+1
			token, w := koNodeToken(team)
			st := labelStyle
			switch {
			case c < width && lit[r][c]:
				st = bracketPathStyle
			case elim[team]:
				st = koOutStyle
			}
			overlayRow[r] = flagOverlay{col: c, width: w, text: st.Render(token)}
		}
	}

	// Style: traced path bright gold, team names bright (a knocked-out club's name
	// darker), connector skeleton dim, and a settled tie's winner advanced as a flag.
	out := make([]string, 0, height+1)
	out = append(out, helpStyle.Render(string(hdr)))
	for r := 0; r < height; r++ {
		nameStyle := labelStyle
		if elimRow[r] {
			nameStyle = koOutStyle
		}
		out = append(out, styleBracketRow(grid[r], lit[r], nameW, nameStyle, overlayRow[r]))
	}
	return out
}

// koTeamNames flattens the bracket leaves into the 32 team labels in bracket
// position order (home, away per tie), falling back to the slot description when
// a team isn't resolved yet — the same ordering bracketLines lays out.
func koTeamNames(leaves []standings.ProjMatch) []string {
	names := make([]string, 0, len(leaves)*2)
	for _, lf := range leaves {
		h, a := lf.HomeTeam, lf.AwayTeam
		if h == "" {
			h = lf.HomeDesc
		}
		if a == "" {
			a = lf.AwayDesc
		}
		names = append(names, h, a)
	}
	return names
}

// styleBracketRow renders one bracket grid row, coalescing runs of equally-styled
// cells: lit cells in the traced-path accent, otherwise the team name (left of
// nameW) in nameStyle (dimmed for a knocked-out club) and the connector skeleton
// (right) dim. ov, when non-zero, splices a pre-styled winner token in at ov.col,
// consuming ov.width grid cells so the token's display width replaces that many
// connector cells and the row stays aligned.
func styleBracketRow(row []rune, lit []bool, nameW int, nameStyle lipgloss.Style, ov flagOverlay) string {
	styles := []lipgloss.Style{nameStyle, helpStyle, bracketPathStyle}
	kind := func(c int) int {
		switch {
		case lit[c]:
			return 2
		case c < nameW:
			return 0
		default:
			return 1
		}
	}
	hasOverlay := ov.text != ""
	var b strings.Builder
	for c := 0; c < len(row); {
		if hasOverlay && c == ov.col {
			b.WriteString(ov.text)
			c += ov.width
			continue
		}
		k := kind(c)
		j := c
		for j < len(row) && kind(j) == k && !(hasOverlay && j == ov.col) {
			j++
		}
		b.WriteString(styles[k].Render(string(row[c:j])))
		c = j
	}
	return b.String()
}

// findKOTie returns the entered match in ms whose two teams are exactly {a, b}
// (either home/away order), or nil — used to advance a bracket node once both its
// feeder winners are known.
func findKOTie(ms []models.Match, a, b string) *models.Match {
	for i := range ms {
		if (ms[i].TeamA == a && ms[i].TeamB == b) || (ms[i].TeamA == b && ms[i].TeamB == a) {
			return &ms[i]
		}
	}
	return nil
}

// koNodeToken renders a club advanced into an inner bracket node as a compact
// token plus its display width: the flag emoji (2 columns) when known, otherwise
// a short letters-only abbreviation so an unmapped club is still identifiable.
func koNodeToken(team string) (string, int) {
	if f, ok := teamFlags[team]; ok {
		return f, 2
	}
	ab := koAbbrev(team)
	return ab, len([]rune(ab))
}

// koAbbrev is the up-to-three-letter uppercase fallback for a club with no flag
// (e.g. "DR Congo" -> "DRC"), so inner-round nodes never render a meaningless
// white flag.
func koAbbrev(team string) string {
	letters := make([]rune, 0, 3)
	for _, r := range team {
		if unicode.IsLetter(r) {
			letters = append(letters, unicode.ToUpper(r))
			if len(letters) == 3 {
				break
			}
		}
	}
	if len(letters) == 0 {
		return "??"
	}
	return string(letters)
}

// koMatchLine renders one bracket match: the final score if played, the live
// score if in play, otherwise the kickoff time. A knocked-out club (in elim) is
// dimmed, so the loser of a settled tie reads as out.
func koMatchLine(mt models.Match, elim map[string]bool) string {
	team := func(name string) string {
		t := truncate(name, 16)
		if elim[name] {
			return koOutStyle.Render(t)
		}
		return t
	}
	switch {
	case mt.Finished && mt.ScoreA != nil:
		pens := ""
		if mt.PenA != nil && mt.PenB != nil {
			pens = helpStyle.Render(fmt.Sprintf("  (pens %d-%d)", *mt.PenA, *mt.PenB))
		}
		return fmt.Sprintf("%s %s %s", team(mt.TeamA), okStyle.Render(fmtResult(mt)), team(mt.TeamB)) + pens
	case mt.Live:
		return fmt.Sprintf("%s %s %s", team(mt.TeamA), liveScore(mt), team(mt.TeamB))
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

