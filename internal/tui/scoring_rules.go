package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/scoring"
)

// updateScoringRules handles the read-only "How scoring works" screen: any key
// returns to the menu (q quits), like the other informational screens.
func (m Model) updateScoringRules(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "q":
			return m, tea.Quit
		default:
			return m.goMenu(), nil
		}
	}
	return m, nil
}

// viewScoringRules explains the currently active scoring mode, with a worked
// example so players know exactly how points are earned.
func (m Model) viewScoringRules() string {
	out := titleStyle.Render("How scoring works") + "\n\n"
	out += labelStyle.Render("Active mode: ") + okStyle.Render(m.scoringMode.Label()) + "\n\n"

	switch m.scoringMode {
	case scoring.ModeProximity:
		out += rule("Exact score", "5 points")
		out += rule("Correct result, 1 goal off", "4 points")
		out += rule("Correct result, 2 goals off", "3 points")
		out += rule("Correct result, further off", "down to 1 point")
		out += rule("Wrong result (W/D/L)", "0 points")
		out += "\n" + helpStyle.Render("You lose 1 point for every goal your scoreline is off,") + "\n"
		out += helpStyle.Render("but always keep at least 1 for calling the winner.") + "\n"
		out += "\n" + exampleProximity()
	case scoring.ModeScarcity:
		out += helpStyle.Render("Base points (the closer the scoreline, the more):") + "\n"
		out += rule("Exact score", "5 points")
		out += rule("Correct result, 1 goal off", "4 points")
		out += rule("Correct result, further off", "down to 1 point")
		out += rule("Wrong result (W/D/L)", "0 points")
		out += "\n" + helpStyle.Render("Contrarian bonus (on correct picks others missed):") + "\n"
		out += rule("Result <25% of players called", "+2 points")
		out += rule("Exact score <10% of players called", "+2 points")
		out += "\n" + helpStyle.Render("The bonus needs a crowd: it only kicks in once at least 8") + "\n"
		out += helpStyle.Render("players have bet a match. Thinner matches use base points only.") + "\n"
		out += "\n" + exampleScarcity()
	default: // Classic
		out += rule("Exact score", "3 points")
		out += rule("Correct result only (W/D/L)", "1 point")
		out += rule("Wrong result", "0 points")
		out += "\n" + helpStyle.Render("An exact score is worth 3 — there is no extra over/under bonus.") + "\n"
	}

	if m.roundWeights != scoring.WeightFlat {
		out += "\n" + ruleLadder(m.roundWeights)
	}

	out += "\n" + helpStyle.Render("Knockouts use the 90-minute score (a 1-1 a.e.t. counts as a 1-1 draw).") + "\n"
	out += "\n" + statusLine(m) + helpStyle.Render("any key: back · q: quit")
	return out
}

// ruleLadder renders the active round-weight ladder: which phases multiply the
// points above, so players see exactly how much more knockouts are worth.
func ruleLadder(w scoring.WeightScheme) string {
	out := helpStyle.Render("Round weights — later rounds multiply the points above:") + "\n"
	for _, e := range w.Ladder() {
		out += rule(e.Phase.Label(), fmt.Sprintf("×%d", e.Mult))
	}
	return out
}

// rule renders one "label … value" line of a scoring table.
func rule(label, value string) string {
	return "  " + labelStyle.Render(label) + "  " + okStyle.Render(value) + "\n"
}

func exampleProximity() string {
	out := helpStyle.Render("Example — final score 4-1:") + "\n"
	out += "  " + labelStyle.Render("you bet 4-1") + "  " + okStyle.Render("5") + helpStyle.Render(" (exact)") + "\n"
	out += "  " + labelStyle.Render("you bet 3-1") + "  " + okStyle.Render("4") + helpStyle.Render(" (1 goal off)") + "\n"
	out += "  " + labelStyle.Render("you bet 2-0") + "  " + okStyle.Render("2") + helpStyle.Render(" (3 goals off)") + "\n"
	return out
}

func exampleScarcity() string {
	out := helpStyle.Render("Example — final 4-1, and only you called the home win:") + "\n"
	out += "  " + labelStyle.Render("you bet 3-1") + "  " + okStyle.Render("6") + helpStyle.Render(" (4 base + 2 rare-result bonus)") + "\n"
	return out
}
