package tui

import (
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"bethoven/internal/live"
	"bethoven/internal/models"
)

// leaderCommentCol is the column where BETanIA's side commentary starts on the
// leaderboard, leaving the rank/name/pts line in a fixed-width left gutter.
const leaderCommentCol = 38

// leaderCommentMaxWidth caps the comment column so takes wrap into a squarer,
// readable block instead of one very long line on a wide terminal.
const leaderCommentMaxWidth = 54

// leaderTickMsg drives the live leaderboard's auto-refresh. epoch ties a tick to
// the visit that scheduled it, so a stale loop from a prior visit is ignored.
type leaderTickMsg struct{ epoch int }

// leaderRefresh is how often the live leaderboard re-polls while it's open.
const leaderRefresh = 20 * time.Second

// leaderTick schedules the next leaderboard refresh for the given epoch.
func leaderTick(epoch int) tea.Cmd {
	return tea.Tick(leaderRefresh, func(time.Time) tea.Msg { return leaderTickMsg{epoch} })
}

// cycleTickMsg drives the admin comment-cycle: each tick advances which player's
// comment is shown. epoch ties the tick to the toggle session that scheduled it.
type cycleTickMsg struct{ epoch int }

// cycleRefresh is how often the leaderboard rotates to the next player's comment
// while cycling is on — paced so there's time to actually read each line.
const cycleRefresh = 12 * time.Second

// cycleTick schedules the next comment-cycle advance for the given epoch.
func cycleTick(epoch int) tea.Cmd {
	return tea.Tick(cycleRefresh, func(time.Time) tea.Msg { return cycleTickMsg{epoch} })
}

// cycleCandidates returns the user ids that have a (non-empty) comment to cycle
// through, in standings order, so cycling follows the visible board.
func (m Model) cycleCandidates() []int64 {
	out := make([]int64, 0, len(m.cycleAll))
	for _, s := range m.standings {
		if m.cycleAll[s.User.ID] != "" {
			out = append(out, s.User.ID)
		}
	}
	// Any commented player not in standings (shouldn't happen) still gets a turn.
	if len(out) != len(m.cycleAll) {
		seen := make(map[int64]bool, len(out))
		for _, id := range out {
			seen[id] = true
		}
		extra := make([]int64, 0)
		for id, txt := range m.cycleAll {
			if txt != "" && !seen[id] {
				extra = append(extra, id)
			}
		}
		sort.Slice(extra, func(i, j int) bool { return extra[i] < extra[j] })
		out = append(out, extra...)
	}
	return out
}

// startCycle turns the comment cycle on: loads everyone's (non-muted) comments,
// shows the viewer's own first, and begins the auto-advance loop. No-op for muted
// viewers — they don't get the cycle.
func (m Model) startCycle() (Model, tea.Cmd) {
	if m.selfMuted {
		return m, nil
	}
	m.cycleAll = m.svc.AllLeaderboardComments()
	m.cycleComments = true
	// Start on the viewer's own comment if they have one, else the first candidate.
	m.cycleCurrentID = 0
	if m.user != nil && m.cycleAll[m.user.ID] != "" {
		m.cycleCurrentID = m.user.ID
	} else if cands := m.cycleCandidates(); len(cands) > 0 {
		m.cycleCurrentID = cands[0]
	}
	m.cycleEpoch++
	return m, cycleTick(m.cycleEpoch)
}

// stopCycle turns the cycle off and supersedes its tick loop.
func (m Model) stopCycle() Model {
	m.cycleComments = false
	m.cycleAll = nil
	m.cycleCurrentID = 0
	m.cycleEpoch++ // any in-flight tick is now stale and self-stops
	return m
}

// onCycleTick advances to the next player's comment and reschedules — but only
// while the leaderboard is active, the cycle is on, and the tick is current.
func (m Model) onCycleTick(msg cycleTickMsg) (tea.Model, tea.Cmd) {
	if m.screen != screenLeaderboard || !m.cycleComments || msg.epoch != m.cycleEpoch {
		return m, nil
	}
	// Refresh the set so newly (re)generated comments are picked up live.
	m.cycleAll = m.svc.AllLeaderboardComments()
	cands := m.cycleCandidates()
	switch len(cands) {
	case 0:
		m.cycleCurrentID = 0
	case 1:
		m.cycleCurrentID = cands[0]
	default:
		// Random next, never repeating the current one — "another player at random".
		next := m.cycleCurrentID
		for next == m.cycleCurrentID {
			next = cands[rand.Intn(len(cands))]
		}
		m.cycleCurrentID = next
	}
	return m, cycleTick(msg.epoch)
}

// onLeaderTick re-fetches the standings + in-play matches and reschedules — but
// only while the leaderboard is the active screen AND the tick belongs to the
// current visit, so leaving (or re-entering) the screen never leaves overlapping
// refresh loops running.
func (m Model) onLeaderTick(msg leaderTickMsg) (tea.Model, tea.Cmd) {
	if m.screen != screenLeaderboard || msg.epoch != m.leaderEpoch {
		return m, nil
	}
	if board, err := m.svc.Leaderboard(); err == nil {
		m.standings = board
	}
	m.rowComments = m.svc.LeaderboardComments(m.user)
	m.liveMatches, _ = m.svc.LiveMatches()
	m.liveCommentary = m.svc.LiveCommentary()
	if m.revealLivePicks {
		m.livePicks, _ = m.svc.LivePicks()
	}
	return m, leaderTick(msg.epoch)
}

// updateLeaderboard handles the live leaderboard: 'p' toggles the per-match pick
// reveal (open to all, in-play matches only); any other key returns to the menu.
func (m Model) updateLeaderboard(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "p":
		m.revealLivePicks = !m.revealLivePicks
		if m.revealLivePicks {
			m.livePicks, _ = m.svc.LivePicks()
		} else {
			m.livePicks = nil
		}
		return m, nil
	case "h":
		// Toggle the viewer's own "hide comments" preference (persisted). Hiding also
		// tears down the cycle; un-hiding restarts it (unless the viewer is muted).
		m.hideComments = !m.hideComments
		if err := m.svc.SetLeaderboardCommentsHidden(m.user, m.hideComments); err != nil {
			m.setStatus(err.Error(), true)
		}
		if m.hideComments {
			return m.stopCycle(), nil
		}
		return m.startCycle()
	case "c":
		// Toggle the comment cycle. Not a control when comments are hidden or the
		// viewer is muted — fall through to the default (back to menu).
		if m.selfMuted || m.hideComments {
			return m.goMenu(), nil
		}
		if m.cycleComments {
			return m.stopCycle(), nil
		}
		return m.startCycle()
	default:
		m = m.stopCycle() // leaving the screen tears down the cycle loop
		return m.goMenu(), nil
	}
}

// wrapText word-wraps s to at most width columns, returning one string per line.
// width is approximate (it counts bytes, not display cells) — fine for the short
// comment lines it wraps. A non-positive/small width falls back to 60.
func wrapText(s string, width int) []string {
	if width < 10 {
		width = 60
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	lines := make([]string, 0, 2)
	cur := words[0]
	for _, w := range words[1:] {
		if len(cur)+1+len(w) > width {
			lines = append(lines, cur)
			cur = w
		} else {
			cur += " " + w
		}
	}
	return append(lines, cur)
}

// liveScore renders a match's running score with the live accent, e.g.
// "⚡67' 1–0" (or "⚡ 1–0" when the feed gives no clock). At halftime the clock
// reads a stale stoppage time like "45'+8'", so show "HT" instead — clearer that
// play is paused. Caller guarantees mt.Live.
func liveScore(mt models.Match) string {
	label := mt.LiveClock
	if mt.LivePhase == live.PhaseHalftime {
		label = "HT"
	}
	prefix := "⚡"
	if label != "" {
		prefix = "⚡" + label + " "
	}
	return liveStyle.Render(fmt.Sprintf("%s%d-%d", prefix, mt.LiveScoreA, mt.LiveScoreB))
}

// fmtPick renders a player's pick compactly, e.g. "2-1".
func fmtPick(b *models.Bet) string {
	if b == nil {
		return "—"
	}
	return fmt.Sprintf("%d-%d", b.PredA, b.PredB)
}

// fmtResult renders a match's final score, or "—" if not played.
func fmtResult(mt models.Match) string {
	if mt.Finished && mt.ScoreA != nil {
		return fmt.Sprintf("%d-%d", *mt.ScoreA, *mt.ScoreB)
	}
	return "—"
}

func (m Model) viewMyResults() string {
	// Show only matches the player actually bet on (skip the un-bet rows).
	placed := 0
	for _, r := range m.myRows {
		if r.Bet != nil {
			placed++
		}
	}

	out := titleStyle.Render("My bets") +
		labelStyle.Render(fmt.Sprintf("   (%d placed · %s pts)", placed, okStyle.Render(strconv.Itoa(m.myTotal)))) + "\n\n"

	if placed == 0 {
		out += helpStyle.Render("  No bets yet — pick some matches from the menu.\n")
		out += "\n" + helpStyle.Render("any key: back · q: quit")
		return out
	}

	out += labelStyle.Render(fmt.Sprintf("  %-30s %-8s %-7s %s", "match", "my pick", "result", "pts")) + "\n"
	anyLive := false
	for _, r := range m.myRows {
		if r.Bet == nil {
			continue
		}
		// 13 + " v " (3) + 14 = 30 display cols, matching the "%-30s" header.
		match := teamCell(r.Match.TeamA, 13) + " v " + teamCell(r.Match.TeamB, 14)
		// In play: show the running score (styled) in place of result/pts.
		if r.Match.Live {
			anyLive = true
			out += fmt.Sprintf("  %s %-8s %s\n", match, fmtPick(r.Bet), liveScore(r.Match))
			continue
		}
		pts := "·"
		if r.Match.Finished {
			pts = strconv.Itoa(r.Points)
		}
		out += fmt.Sprintf("  %s %-8s %-7s %s\n", match, fmtPick(r.Bet), fmtResult(r.Match), pts)
	}
	out += "\n"
	if anyLive {
		out += lockStyle.Render(liveLegend) + "\n"
	}
	out += helpStyle.Render("any key: back · q: quit")
	return out
}

func (m Model) viewLeaderboard() string {
	out := titleStyle.Render("🏆  Leaderboard")
	if len(m.liveMatches) > 0 {
		out += "  " + liveStyle.Render("● LIVE")
	}
	out += "\n\n"

	// In-play header: the matches currently feeding provisional points. With the
	// pick reveal on ('p'), each match expands to show every player's pick.
	if len(m.liveMatches) > 0 {
		// BETanIA's running take on the in-play slate, shown FIRST as a headline so
		// it's never buried under expanded picks. Always shown when present: this is
		// general live commentary, not a per-player take, so the per-viewer comment
		// hide ('h') deliberately does NOT cover it.
		if m.liveCommentary != "" {
			width := m.width - 6
			if width > leaderCommentMaxWidth {
				width = leaderCommentMaxWidth
			}
			for i, ln := range wrapText(m.liveCommentary, width) {
				prefix := "  " + botMark.Render("🤖") + " "
				if i > 0 {
					prefix = "     " // align continuation past the 🤖
				}
				out += prefix + commentStyle.Render(ln) + "\n"
			}
			out += "\n"
		}
		for _, mt := range m.liveMatches {
			out += "  " + liveScore(mt) +
				labelStyle.Render(fmt.Sprintf("  %s v %s", mt.TeamA, mt.TeamB)) + "\n"
			if m.revealLivePicks {
				out += m.liveMatchPicks(mt.ID)
			}
		}
		out += "\n"
	}

	if len(m.standings) == 0 {
		out += helpStyle.Render("No players yet.\n")
	}
	anyLive := false
	for i, s := range m.standings {
		rank := fmt.Sprintf("%2d.", i+1)
		// Mark a total that currently includes provisional (in-play) points.
		marker := " "
		if s.LivePoints > 0 {
			marker = liveStyle.Render("⚡")
			anyLive = true
		}
		line := fmt.Sprintf("%s %-20s %3d pts", rank, s.User.DisplayName, s.Total)
		switch {
		case s.LivePoints > 0:
			line = liveStyle.Render(line)
		case i == 0 && s.Total > 0:
			line = cursorOn.Render(line)
		default:
			line = labelStyle.Render(line)
		}
		// Points gained from live matches + rank shift they caused, rendered as
		// independent segments so their colors don't nest inside the line style.
		if s.LivePoints > 0 {
			line += liveStyle.Render(fmt.Sprintf(" (+%d)", s.LivePoints))
		}
		switch {
		case s.LiveRankDelta > 0:
			line += okStyle.Render(" ▲")
		case s.LiveRankDelta < 0:
			line += errStyle.Render(" ▼")
		}
		row := "  " + marker + line
		// BETanIA's take, shown in the terminal's default colour (italic) so it's
		// legible on any theme, laid out in a right-hand column to keep the board
		// uncluttered (falling back to stacking under the row on a narrow terminal),
		// width-capped so it wraps into a squarer block. Source: nothing when the
		// viewer hid comments ('h'); the single rotating player while cycling; else
		// the viewer's own comment.
		var c string
		switch {
		case m.hideComments:
			c = ""
		case m.cycleComments:
			if s.User.ID == m.cycleCurrentID {
				c = m.cycleAll[s.User.ID]
			}
		default:
			c = m.rowComments[s.User.ID]
		}
		commentWidth := m.width - leaderCommentCol - 3
		if commentWidth > leaderCommentMaxWidth {
			commentWidth = leaderCommentMaxWidth
		}
		narrowWidth := m.width - 9
		if narrowWidth > leaderCommentMaxWidth {
			narrowWidth = leaderCommentMaxWidth
		}
		switch {
		case c == "":
			out += row + "\n"
		case commentWidth < 24:
			out += row + "\n"
			for i, ln := range wrapText(c, narrowWidth) {
				prefix := "      " + botMark.Render("🤖") + " "
				if i > 0 {
					prefix = "         " // align continuation under the text, past the 🤖
				}
				out += prefix + commentStyle.Render(ln) + "\n"
			}
		default:
			lines := wrapText(c, commentWidth)
			pad := leaderCommentCol - lipgloss.Width(row)
			if pad < 1 {
				pad = 1
			}
			out += row + strings.Repeat(" ", pad) + botMark.Render("🤖") + " " + commentStyle.Render(lines[0]) + "\n"
			indent := strings.Repeat(" ", leaderCommentCol+3) // align continuation past the 🤖
			for _, ln := range lines[1:] {
				out += indent + commentStyle.Render(ln) + "\n"
			}
		}
	}

	out += "\n"
	if anyLive || len(m.liveMatches) > 0 {
		out += lockStyle.Render(liveLegend) + "\n"
		out += lockStyle.Render("▲▼ rank shift from live results · (+N) points gained live") + "\n"
	}
	switch {
	case m.hideComments:
		// nothing — comments are hidden by the viewer's choice
	case m.cycleComments:
		who := "…"
		if name := m.cycleName(m.cycleCurrentID); name != "" {
			who = name
		}
		out += botMark.Render("🤖") + commentStyle.Render(" cycling takes — showing "+who) + "\n"
	case len(m.rowComments) > 0:
		out += botMark.Render("🤖") + commentStyle.Render(" BETanIA's take") + "\n"
	}

	// Help line: live-pick toggle + comment controls (cycle + show/hide).
	var hints []string
	if len(m.liveMatches) > 0 {
		if m.revealLivePicks {
			hints = append(hints, "p: hide picks")
		} else {
			hints = append(hints, "p: reveal live picks")
		}
	}
	switch {
	case m.hideComments:
		hints = append(hints, "h: show comments")
	case !m.selfMuted:
		if m.cycleComments {
			hints = append(hints, "c: stop cycling comments")
		} else {
			hints = append(hints, "c: cycle comments")
		}
		hints = append(hints, "h: hide comments")
	}
	hints = append(hints, "other: back", "q: quit")
	out += helpStyle.Render(strings.Join(hints, " · "))
	return out
}

// cycleName resolves a user id to a display name from the current standings.
func (m Model) cycleName(id int64) string {
	for _, s := range m.standings {
		if s.User.ID == id {
			return s.User.DisplayName
		}
	}
	return ""
}

// liveMatchPicks renders every player's revealed pick for one in-play match
// (pick + provisional points), indented under its live score line.
func (m Model) liveMatchPicks(matchID int64) string {
	for i := range m.livePicks {
		if m.livePicks[i].Match.ID != matchID {
			continue
		}
		picks := m.livePicks[i].Picks
		if len(picks) == 0 {
			break
		}
		out := ""
		for _, p := range picks {
			pick := p.Bet // address a local copy for fmtPick
			gain := helpStyle.Render(fmt.Sprintf("+%d", p.LivePoints))
			if p.LivePoints > 0 {
				gain = liveStyle.Render(fmt.Sprintf("+%d", p.LivePoints))
			}
			out += "      " +
				labelStyle.Render(fmt.Sprintf("%-20s %-4s ", p.User.DisplayName, fmtPick(&pick))) +
				gain + "\n"
		}
		return out
	}
	return helpStyle.Render("      (no picks yet)") + "\n"
}
