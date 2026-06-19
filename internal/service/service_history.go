package service

import (
	"sort"

	"bethoven/internal/ai"
	"bethoven/internal/models"
	"bethoven/internal/scoring"
)

// StandingsHistory reconstructs the leaderboard after each matchday from FINISHED
// matches only, oldest→newest. It stores nothing: scores and positions are a pure
// fold of persisted bets + results — exactly what Leaderboard computes live — just
// run once per round with a kickoff-date cutoff. Per-round Movement / PointsGained
// are derived here so BETanIA's narrative layer gets grounded numbers rather than
// guesses. Returns an empty slice when no match has finished yet.
//
// Rounds are keyed by the UTC date of kickoff (matches are chronological), which
// is the competition timeline; editing a past result simply re-derives history.
func (s *Service) StandingsHistory() ([]ai.RoundStanding, error) {
	users, err := s.store.AllUsers()
	if err != nil {
		return nil, err
	}
	matches, err := s.store.ListMatches(s.tournamentID)
	if err != nil {
		return nil, err
	}
	bets, err := s.store.AllBets(s.tournamentID)
	if err != nil {
		return nil, err
	}
	sc, err := s.newScorer()
	if err != nil {
		return nil, err
	}

	betsByMatch := make(map[int64][]models.Bet)
	for _, b := range bets {
		betsByMatch[b.MatchID] = append(betsByMatch[b.MatchID], b)
	}

	// Finished matches in chronological order define the rounds.
	finished := make([]models.Match, 0, len(matches))
	for _, m := range matches {
		if m.Finished {
			finished = append(finished, m)
		}
	}
	sort.Slice(finished, func(i, j int) bool { return finished[i].StartsAt.Before(finished[j].StartsAt) })
	if len(finished) == 0 {
		return nil, nil
	}

	// Distinct kickoff dates (UTC) = round boundaries, in order.
	var dates []string
	seen := make(map[string]bool)
	for _, m := range finished {
		d := m.StartsAt.UTC().Format("2006-01-02")
		if !seen[d] {
			seen[d] = true
			dates = append(dates, d)
		}
	}

	totals := make(map[int64]int)
	// Tiebreaker tallies, accumulated alongside totals so each round's ranking uses
	// the same exact→result→name order as the live board (history is settled, so
	// there is no live nuance here).
	exact := make(map[int64]int)
	result := make(map[int64]int)
	prevPos := make(map[int64]int)
	prevTotal := make(map[int64]int)
	mi := 0 // running index into finished
	rounds := make([]ai.RoundStanding, 0, len(dates))
	for ri, date := range dates {
		// Fold every finished match whose kickoff date is this round.
		for mi < len(finished) && finished[mi].StartsAt.UTC().Format("2006-01-02") == date {
			m := finished[mi]
			for _, b := range betsByMatch[m.ID] {
				totals[b.UserID] += sc.points(b, m)
				if scoring.IsExact(b, m) {
					exact[b.UserID]++
				}
				if scoring.IsCorrectResult(b, m) {
					result[b.UserID]++
				}
			}
			mi++
		}
		ranked := rankUsers(users, totals, exact, result)
		players := make([]ai.PlayerStanding, 0, len(ranked))
		for pos, u := range ranked {
			pos1 := pos + 1
			ps := ai.PlayerStanding{
				UserID:       u.ID,
				Name:         u.DisplayName,
				Position:     pos1,
				Total:        totals[u.ID],
				PointsGained: totals[u.ID] - prevTotal[u.ID], // prevTotal is 0 at round 1
			}
			if ri > 0 {
				ps.Movement = prevPos[u.ID] - pos1 // + climbed, − fell
			}
			players = append(players, ps)
		}
		// Snapshot this round's positions/totals for the next round's deltas.
		for pos, u := range ranked {
			prevPos[u.ID] = pos + 1
			prevTotal[u.ID] = totals[u.ID]
		}
		rounds = append(rounds, ai.RoundStanding{Label: date, Ranks: players})
	}
	return rounds, nil
}

// rankUsers sorts users by the same rule Leaderboard uses (via betterRank): total
// points, then exact scores, then correct results, then display name — returning
// them in ranked order.
func rankUsers(users []models.User, totals, exact, result map[int64]int) []models.User {
	ranked := make([]models.User, len(users))
	copy(ranked, users)
	sort.Slice(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		return betterRank(
			totals[a.ID], exact[a.ID], result[a.ID], a.DisplayName,
			totals[b.ID], exact[b.ID], result[b.ID], b.DisplayName,
		)
	})
	return ranked
}
