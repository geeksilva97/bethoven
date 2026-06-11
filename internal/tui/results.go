package tui

import (
	"fmt"
	"strconv"

	"bethoven/internal/models"
)

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
	for _, r := range m.myRows {
		if r.Bet == nil {
			continue
		}
		match := fmt.Sprintf("%s v %s", r.Match.TeamA, r.Match.TeamB)
		if len(match) > 30 {
			match = match[:30]
		}
		pts := "·"
		if r.Match.Finished {
			pts = strconv.Itoa(r.Points)
		}
		out += fmt.Sprintf("  %-30s %-8s %-7s %s\n", match, fmtPick(r.Bet), fmtResult(r.Match), pts)
	}
	out += "\n" + helpStyle.Render("any key: back · q: quit")
	return out
}

func (m Model) viewLeaderboard() string {
	out := titleStyle.Render("🏆  Leaderboard") + "\n\n"
	if len(m.standings) == 0 {
		out += helpStyle.Render("No players yet.\n")
	}
	for i, s := range m.standings {
		rank := fmt.Sprintf("%2d.", i+1)
		line := fmt.Sprintf("%s %-20s %3d pts", rank, s.User.DisplayName, s.Total)
		if i == 0 && s.Total > 0 {
			line = cursorOn.Render(line)
		} else {
			line = labelStyle.Render(line)
		}
		out += "  " + line + "\n"
	}
	out += "\n" + helpStyle.Render("any key: back · q: quit")
	return out
}
