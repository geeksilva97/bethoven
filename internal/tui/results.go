package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/models"
)

// leaderTickMsg drives the live leaderboard's auto-refresh. epoch ties a tick to
// the visit that scheduled it, so a stale loop from a prior visit is ignored.
type leaderTickMsg struct{ epoch int }

// leaderRefresh is how often the live leaderboard re-polls while it's open.
const leaderRefresh = 20 * time.Second

// leaderTick schedules the next leaderboard refresh for the given epoch.
func leaderTick(epoch int) tea.Cmd {
	return tea.Tick(leaderRefresh, func(time.Time) tea.Msg { return leaderTickMsg{epoch} })
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
	default:
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
// "⚡67' 1–0" (or "⚡ 1–0" when the feed gives no clock). Caller guarantees mt.Live.
func liveScore(mt models.Match) string {
	prefix := "⚡"
	if mt.LiveClock != "" {
		prefix = "⚡" + mt.LiveClock + " "
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
		out += "  " + marker + line + "\n"
		// BETanIA's take, indented under the row. Scoped by the service: a player
		// sees only their own line, an admin sees everyone's. Rendered in the
		// terminal's default colour (italic) so it's legible on light and dark themes.
		if c := m.rowComments[s.User.ID]; c != "" {
			lines := wrapText(c, m.width-9)
			for i, ln := range lines {
				prefix := "      " + botMark.Render("🤖") + " "
				if i > 0 {
					prefix = "         " // align continuation under the text, past the 🤖
				}
				out += prefix + commentStyle.Render(ln) + "\n"
			}
		}
	}

	out += "\n"
	if anyLive || len(m.liveMatches) > 0 {
		out += lockStyle.Render(liveLegend) + "\n"
		out += lockStyle.Render("▲▼ rank shift from live results · (+N) points gained live") + "\n"
	}
	if len(m.rowComments) > 0 {
		out += botMark.Render("🤖") + commentStyle.Render(" BETanIA's take") + "\n"
	}
	help := "any key: back · q: quit"
	if len(m.liveMatches) > 0 {
		if m.revealLivePicks {
			help = "p: hide picks · other: back · q: quit"
		} else {
			help = "p: reveal live picks · other: back · q: quit"
		}
	}
	out += helpStyle.Render(help)
	return out
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
