package service

import (
	"sort"

	"bethoven/internal/live"
	"bethoven/internal/models"
	"bethoven/internal/standings"
)

// knockoutPhases is the bracket ladder in tournament order. The picture always
// lists every round (even before it is drawn) so the screen can show the full
// scaffold with "not drawn yet" placeholders.
var knockoutPhases = []models.Phase{
	models.PhaseRound32,
	models.PhaseRound16,
	models.PhaseRound8,
	models.PhaseSemi,
	models.PhaseFinal,
}

// KnockoutPicture is the read-only state for the player-facing "Knockouts"
// screen: live-computed group tables, the cross-group race for the eight best
// third-placed spots, and the knockout bracket as the admin has entered it. It
// reveals no individual picks — purely tournament state — so it needs no admin
// gate. Computed entirely from ListMatches; nothing here is persisted.
type KnockoutPicture struct {
	Groups     []standings.Group
	ThirdPlace []standings.ThirdPlace
	Bracket    []BracketRound
	// Projected is the Round-of-32 bracket inferred from the current group
	// tables — "if the group stage ended now, who plays whom". It fills the R32
	// slots from current standings; the UI shows it only until real knockout
	// matchups are entered. A projection, not necessarily FIFA's official table.
	Projected []standings.ProjMatch
	// Eliminated is the set of knockout teams that are out, so the UI can dim
	// them. Conservative and never inferred from a 90' draw — see eliminatedTeams.
	Eliminated map[string]bool
}

// BracketRound is one knockout phase and the matches entered for it (live scores
// overlaid). Empty Matches ⇒ that round has not been drawn yet — knockout
// matchups are added by the admin as they are decided, so the bracket is exactly
// the set of entered matches; advancement is never inferred (a level 90' result
// can't reveal a penalty-shootout winner, and only the 90' score is stored).
type BracketRound struct {
	Phase   models.Phase
	Label   string
	Matches []models.Match
}

// KnockoutPicture assembles the group tables, third-place race, and bracket. The
// group computation folds in-play matches provisionally — exactly like the
// leaderboard — by treating each live score as final on a synthetic copy, so the
// table reflects the current state of play. Knockout matches are excluded from
// the group math (the pure package ignores non-group phases) and surfaced only in
// the bracket.
func (s *Service) KnockoutPicture() (KnockoutPicture, error) {
	matches, err := s.store.ListMatches(s.tournamentID)
	if err != nil {
		return KnockoutPicture{}, err
	}
	snap := s.liveSnapshot()

	groupInput := make([]models.Match, len(matches))
	for i, m := range matches {
		groupInput[i] = liveFinal(m, snap)
	}
	groups := standings.GroupStandings(groupInput)
	thirds := standings.ThirdPlaceRace(groups)

	bracket := buildBracket(matches, snap)
	return KnockoutPicture{
		Groups:     groups,
		ThirdPlace: thirds,
		Bracket:    bracket,
		Projected:  standings.ProjectR32(groups, thirds),
		Eliminated: eliminatedTeams(bracket),
	}, nil
}

// eliminatedTeams returns the set of knockout clubs that are out. A club is dimmed
// only on hard evidence from a FINISHED tie — never from an upcoming one, so a club
// still waiting to play its round is never greyed out:
//   - it lost a finished tie with a known winner — a decisive 90' score or a
//     recorded penalty shootout on a 90' draw (see koLoser); or
//   - it drew a finished tie at 90' with NO shootout recorded yet, but its opponent
//     has since been drawn into a later round (so the opponent advanced and this
//     club is out). If neither side of an unresolved draw has advanced yet, the
//     winner is unknown and neither is dimmed.
//
// Advancement is never inferred from an UNFINISHED match — only finished ties feed
// either rule. (This is the fix for the bug where entering the first R16 matchup
// greyed out every R32 club whose own tie had not yet been played: the old rule
// dimmed any club absent from the furthest drawn round, ignoring whether its own
// tie was even over.)
func eliminatedTeams(bracket []BracketRound) map[string]bool {
	out := map[string]bool{}
	// Which clubs appear in each round's ENTERED matches, by ladder index.
	inRound := make([]map[string]bool, len(bracket))
	for i, rd := range bracket {
		inRound[i] = make(map[string]bool, len(rd.Matches)*2)
		for _, mt := range rd.Matches {
			inRound[i][mt.TeamA] = true
			inRound[i][mt.TeamB] = true
		}
	}
	appearsLater := func(team string, after int) bool {
		for j := after + 1; j < len(bracket); j++ {
			if inRound[j][team] {
				return true
			}
		}
		return false
	}
	for i, rd := range bracket {
		for _, mt := range rd.Matches {
			if loser := koLoser(mt); loser != "" {
				out[loser] = true // decisive 90' or recorded shootout
				continue
			}
			// A finished, still-level tie (shootout not entered yet): resolve by
			// advancement — whichever side was drawn into a later round won, so the
			// other is out. An UNFINISHED tie falls through and eliminates nobody.
			if mt.Finished && mt.ScoreA != nil && mt.ScoreB != nil && *mt.ScoreA == *mt.ScoreB {
				aLater, bLater := appearsLater(mt.TeamA, i), appearsLater(mt.TeamB, i)
				switch {
				case aLater && !bLater:
					out[mt.TeamB] = true
				case bLater && !aLater:
					out[mt.TeamA] = true
				}
			}
		}
	}
	return out
}

// KOResult reports the winner and loser of a finished knockout tie. decided is
// false when the tie can't be resolved yet: it's unfinished, or it ended level
// at 90' with no penalty shootout recorded — advancement is never inferred from
// the level 90' score alone. A decisive 90' score names the winner directly; a
// 90' draw is resolved by the recorded shootout (PenA/PenB).
func KOResult(mt models.Match) (winner, loser string, decided bool) {
	if !mt.Finished || mt.ScoreA == nil || mt.ScoreB == nil {
		return "", "", false
	}
	switch {
	case *mt.ScoreA > *mt.ScoreB:
		return mt.TeamA, mt.TeamB, true
	case *mt.ScoreB > *mt.ScoreA:
		return mt.TeamB, mt.TeamA, true
	case mt.PenA != nil && mt.PenB != nil && *mt.PenA != *mt.PenB:
		if *mt.PenA > *mt.PenB {
			return mt.TeamA, mt.TeamB, true
		}
		return mt.TeamB, mt.TeamA, true
	}
	return "", "", false
}

// koLoser names the losing club of a finished knockout tie, or "" if it can't be
// decided yet (see KOResult). Used by eliminatedTeams to dim the club that's out.
func koLoser(mt models.Match) string {
	_, loser, _ := KOResult(mt)
	return loser
}

// liveFinal returns a copy of m with the in-play score baked in as the final
// result, so the pure standings package (which reads only ScoreA/ScoreB +
// Finished) scores it provisionally. Finished matches and matches with no live
// entry are returned unchanged.
func liveFinal(m models.Match, snap map[int64]live.Score) models.Match {
	if m.Finished || snap == nil {
		return m
	}
	ls, ok := snap[m.ID]
	if !ok || ls.State != live.StateIn {
		return m
	}
	a, b := ls.A, ls.B
	m.Finished, m.ScoreA, m.ScoreB = true, &a, &b
	return m
}

// buildBracket groups the non-group matches by phase (in ladder order), each
// round's matches sorted by kickoff, with live scores overlaid for display.
func buildBracket(matches []models.Match, snap map[int64]live.Score) []BracketRound {
	byPhase := map[models.Phase][]models.Match{}
	for _, m := range matches {
		if m.Phase == models.PhaseGroup {
			continue
		}
		mm := m
		overlayLive(&mm, snap)
		byPhase[m.Phase] = append(byPhase[m.Phase], mm)
	}
	rounds := make([]BracketRound, 0, len(knockoutPhases))
	for _, p := range knockoutPhases {
		ms := byPhase[p]
		sort.SliceStable(ms, func(i, j int) bool { return ms[i].StartsAt.Before(ms[j].StartsAt) })
		rounds = append(rounds, BracketRound{Phase: p, Label: p.Label(), Matches: ms})
	}
	return rounds
}
