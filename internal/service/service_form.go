package service

import (
	"bethoven/internal/models"
)

// maxForm is how many recent results the bet screen shows per team.
const maxForm = 5

// SetTeamForms loads each team's pre-tournament recent-form baseline from raw
// W/D/L strings (left = oldest → right = newest; case-insensitive, unknown
// characters ignored). Optional — like SetLiveStore, leaving it unset means the
// bet screen simply shows form derived from tournament results alone. Keeps
// New's signature stable so existing callers/tests are unaffected.
func (s *Service) SetTeamForms(raw map[string]string) {
	forms := make(map[string][]models.FormOutcome, len(raw))
	for name, str := range raw {
		forms[name] = parseForm(str)
	}
	s.forms = forms
}

// parseForm turns a "WWDLW" string into outcomes, ignoring unknown runes.
func parseForm(str string) []models.FormOutcome {
	out := make([]models.FormOutcome, 0, len(str))
	for _, r := range str {
		switch r {
		case 'W', 'w':
			out = append(out, models.FormWin)
		case 'D', 'd':
			out = append(out, models.FormDraw)
		case 'L', 'l':
			out = append(out, models.FormLoss)
		}
	}
	return out
}

// TeamForm returns a team's recent form (oldest→newest, at most maxForm) for the
// bet-screen strip: the seeded pre-tournament baseline with this tournament's
// finished results appended in kickoff order, trimmed to the most recent few.
// Returns nil when the team has neither a baseline nor any finished results.
func (s *Service) TeamForm(team string) []models.FormOutcome {
	out := append([]models.FormOutcome(nil), s.forms[team]...)

	matches, err := s.store.ListMatches(s.tournamentID)
	if err == nil {
		// ListMatches is ordered by kickoff, so finished results append oldest→newest.
		for _, m := range matches {
			if !m.Finished || m.ScoreA == nil || m.ScoreB == nil {
				continue
			}
			switch team {
			case m.TeamA:
				out = append(out, outcomeFor(*m.ScoreA, *m.ScoreB))
			case m.TeamB:
				out = append(out, outcomeFor(*m.ScoreB, *m.ScoreA))
			}
		}
	}

	if len(out) == 0 {
		return nil
	}
	if len(out) > maxForm {
		out = out[len(out)-maxForm:]
	}
	return out
}

// outcomeFor classifies a result from the perspective of the team that scored
// `got` against `against` (regulation 90' scores, so an a.e.t. draw stays a draw).
func outcomeFor(got, against int) models.FormOutcome {
	switch {
	case got > against:
		return models.FormWin
	case got < against:
		return models.FormLoss
	default:
		return models.FormDraw
	}
}
