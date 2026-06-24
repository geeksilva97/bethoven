package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/scoring"
)

// settingsRows is the number of selectable rows on the admin settings screen:
// 0 = public bets, 1 = scoring mode, 2 = round weights.
const settingsRows = 3

// nextMode cycles Classic -> Proximity -> Scarcity -> Classic.
func nextMode(m scoring.Mode) scoring.Mode {
	switch m {
	case scoring.ModeClassic:
		return scoring.ModeProximity
	case scoring.ModeProximity:
		return scoring.ModeScarcity
	default:
		return scoring.ModeClassic
	}
}

// nextWeightScheme cycles Flat -> Knockout -> Doubling -> Linear -> Flat.
func nextWeightScheme(w scoring.WeightScheme) scoring.WeightScheme {
	switch w {
	case scoring.WeightFlat:
		return scoring.WeightKnockout
	case scoring.WeightKnockout:
		return scoring.WeightDoubling
	case scoring.WeightDoubling:
		return scoring.WeightLinear
	default:
		return scoring.WeightFlat
	}
}

// gridTitle is the header for the all-bets grid. The player-facing public view
// drops the admin "⚙" framing.
func (m Model) gridTitle() string {
	if m.gridPublic {
		return "All players' bets"
	}
	return "⚙  All bets"
}

// viewSettings renders the admin settings screen: the public-bets toggle and
// the scoring-mode selector.
func (m Model) viewSettings() string {
	out := titleStyle.Render("⚙  Admin: settings") + "\n\n"

	// Row 0: public bets.
	state := errStyle.Render("OFF")
	if m.publicBets {
		state = okStyle.Render("ON")
	}
	out += settingsRow(m.settingsCursor == 0, "Public bets", state)
	out += helpStyle.Render("    let everyone see others' picks once a match kicks off") + "\n"

	// Row 1: scoring mode.
	out += settingsRow(m.settingsCursor == 1, "Scoring mode", okStyle.Render(m.scoringMode.Label()))
	out += helpStyle.Render("    "+scoringModeHelp(m.scoringMode)) + "\n"

	// Row 2: round weights.
	out += settingsRow(m.settingsCursor == 2, "Round weights", okStyle.Render(m.roundWeights.Label()))
	out += helpStyle.Render("    "+roundWeightsHelp(m.roundWeights)) + "\n"

	out += "\n" + statusLine(m) +
		helpStyle.Render("↑/↓: move · enter/space: toggle/cycle · b/esc: back · q: quit")
	return out
}

// settingsRow renders one selectable settings line with its cursor + value.
func settingsRow(selected bool, label, value string) string {
	cursor := "  "
	name := labelStyle.Render(label)
	if selected {
		cursor = cursorOn.Render("▸ ")
		name = cursorOn.Render(label)
	}
	return cursor + name + "  " + value + "\n"
}

// scoringModeHelp is the one-liner shown under the scoring-mode row.
func scoringModeHelp(m scoring.Mode) string {
	switch m {
	case scoring.ModeProximity:
		return "the closer your scoreline, the more points (max 5)"
	case scoring.ModeScarcity:
		return "Proximity plus a bonus for correct picks few others made"
	default:
		return "exact score 3 · correct result 1 · miss 0"
	}
}

// roundWeightsHelp is the one-liner shown under the round-weights row.
func roundWeightsHelp(w scoring.WeightScheme) string {
	switch w {
	case scoring.WeightKnockout:
		return "knockouts worth more: R16 ×2 · QF/SF ×3 · Final ×4"
	case scoring.WeightDoubling:
		return "knockouts worth more: R16 ×2 · QF ×4 · SF ×8 · Final ×16"
	case scoring.WeightLinear:
		return "knockouts worth more: R16 ×2 · QF ×3 · SF ×4 · Final ×5"
	default:
		return "every match worth the same, whatever the round"
	}
}

// updateSettings handles the settings screen: toggle the selected option.
func (m Model) updateSettings(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "q":
		return m, tea.Quit
	case "esc", "b":
		return m.goMenu(), nil
	case "up", "k":
		if m.settingsCursor > 0 {
			m.settingsCursor--
		}
	case "down", "j":
		if m.settingsCursor < settingsRows-1 {
			m.settingsCursor++
		}
	case "enter", " ":
		switch m.settingsCursor {
		case 0:
			return m.togglePublicBets(), nil
		case 1:
			return m.cycleScoringMode(), nil
		case 2:
			return m.cycleRoundWeights(), nil
		}
	}
	return m, nil
}

// togglePublicBets flips the public-bets setting and refreshes the cached flag.
func (m Model) togglePublicBets() Model {
	if err := m.svc.SetPublicBets(m.user, !m.publicBets); err != nil {
		m.setStatus(err.Error(), true)
		return m
	}
	m.publicBets, _ = m.svc.PublicBetsEnabled()
	if m.publicBets {
		m.setStatus("Public bets enabled — players can now see everyone's picks.", false)
	} else {
		m.setStatus("Public bets disabled.", false)
	}
	return m
}

// cycleScoringMode advances the scoring mode and refreshes the cached value.
func (m Model) cycleScoringMode() Model {
	next := nextMode(m.scoringMode)
	if err := m.svc.SetScoringMode(m.user, next); err != nil {
		m.setStatus(err.Error(), true)
		return m
	}
	m.scoringMode, _ = m.svc.ScoringMode()
	m.setStatus("Scoring mode: "+m.scoringMode.Label(), false)
	return m
}

// cycleRoundWeights advances the round-weight scheme and refreshes the cached value.
func (m Model) cycleRoundWeights() Model {
	next := nextWeightScheme(m.roundWeights)
	if err := m.svc.SetRoundWeights(m.user, next); err != nil {
		m.setStatus(err.Error(), true)
		return m
	}
	m.roundWeights, _ = m.svc.RoundWeights()
	m.setStatus("Round weights: "+m.roundWeights.Label(), false)
	return m
}
