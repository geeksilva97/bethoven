package service

import (
	"bethoven/internal/achievements"
	"bethoven/internal/ai"
	"bethoven/internal/models"
	"bethoven/internal/scoring"
)

// Achievements computes the badge board at read time. UNGATED, like
// AllLeaderboardComments: badges are a shared, fun feature any player can view,
// and every criterion derives from finished matches only, so nothing here can
// leak a pick before kickoff (the same reveal boundary as MatchLeaderboard).
// Nothing is stored — the board re-derives whenever a result lands or is
// edited, so badges can change hands mid-tournament.
func (s *Service) Achievements() (achievements.Board, error) {
	in, err := s.achievementsInput()
	if err != nil {
		return achievements.Board{}, err
	}
	return achievements.Compute(in), nil
}

// achievementsInput flattens the pool into the pure package's input: one
// chronological pick list per player (active-mode points, so badges always
// agree with the leaderboard), their trajectory from StandingsHistory, and
// their participation summary. Mirrors the buildPlayerCards fold — same
// availability rule, same join-date filters — so cards and the Trophy Room can
// never disagree.
func (s *Service) achievementsInput() (achievements.Input, error) {
	users, err := s.store.AllUsers()
	if err != nil {
		return achievements.Input{}, err
	}
	history, err := s.StandingsHistory()
	if err != nil {
		return achievements.Input{}, err
	}
	shares, err := s.resultShares()
	if err != nil {
		return achievements.Input{}, err
	}

	players := make([]achievements.PlayerInput, 0, len(users))
	for _, u := range users {
		rows, _, err := s.MyResults(u.ID)
		if err != nil {
			return achievements.Input{}, err
		}
		p := computeParticipation(rows, u.CreatedAt)
		pi := achievements.PlayerInput{
			UserID: u.ID,
			Name:   u.DisplayName,
			IsAI:   u.Fingerprint == ai.Fingerprint,
			Part: achievements.Participation{
				Available:   p.Available,
				Bet:         p.Bet,
				RecentSkips: p.RecentSkips,
			},
		}

		// Picks: finished matches open to them (kicked off after they joined —
		// the computeParticipation availability rule), chronological. A blank is
		// a Skipped pick so streaks break on it.
		reg := u.CreatedAt
		for _, r := range rows {
			m := r.Match
			if !m.Finished || m.ScoreA == nil || m.ScoreB == nil {
				continue
			}
			if !reg.IsZero() && !m.StartsAt.After(reg) {
				continue // before they joined — not theirs to play
			}
			round := m.StartsAt.UTC().Format("2006-01-02")
			if r.Bet == nil {
				pi.Picks = append(pi.Picks, achievements.Pick{Round: round, Skipped: true, Kickoff: m.StartsAt})
				continue
			}
			b := *r.Bet
			pi.Picks = append(pi.Picks, achievements.Pick{
				Round:       round,
				PredA:       b.PredA,
				PredB:       b.PredB,
				ScoreA:      *m.ScoreA,
				ScoreB:      *m.ScoreB,
				Points:      r.Points,
				Exact:       scoring.IsExact(b, m),
				Correct:     scoring.IsCorrectResult(b, m),
				PlacedAt:    b.CreatedAt,
				UpdatedAt:   b.UpdatedAt,
				Kickoff:     m.StartsAt,
				ResultShare: shares.share(b),
			})
		}

		// Trajectory: this player's rounds from the round they joined, the same
		// regDate filter buildPlayerCards applies (labels are UTC dates, so they
		// compare lexicographically).
		regDate := ""
		if !reg.IsZero() {
			regDate = reg.UTC().Format("2006-01-02")
		}
		for _, round := range history {
			if regDate != "" && round.Label < regDate {
				continue
			}
			for _, ps := range round.Ranks {
				if ps.UserID != u.ID {
					continue
				}
				pi.Rounds = append(pi.Rounds, achievements.RoundDelta{
					Label:        round.Label,
					Position:     ps.Position,
					Movement:     ps.Movement,
					PointsGained: ps.PointsGained,
				})
				break
			}
		}

		players = append(players, pi)
	}
	return achievements.Input{Players: players, TournamentLate: s.tournamentLate()}, nil
}

// pickShares is the per-match W/D/L pick distribution, computed from ALL bets in
// EVERY scoring mode (unlike the scorer's Scarcity-only pools) — The Contrarian
// applies whatever mode is active.
type pickShares struct {
	total  map[int64]int         // matchID -> #bets
	result map[int64]map[int]int // matchID -> W/D/L sign -> #bets
}

func (s *Service) resultShares() (pickShares, error) {
	bets, err := s.store.AllBets(s.tournamentID)
	if err != nil {
		return pickShares{}, err
	}
	ps := pickShares{total: make(map[int64]int), result: make(map[int64]map[int]int)}
	for _, b := range bets {
		ps.total[b.MatchID]++
		if ps.result[b.MatchID] == nil {
			ps.result[b.MatchID] = make(map[int]int)
		}
		ps.result[b.MatchID][scoring.Result(b)]++
	}
	return ps, nil
}

// share returns the fraction of the bet's match's bettors who picked the same
// W/D/L result (including this bet), or -1 below the scarcity quorum — the same
// "rare needs a crowd" gate the Scarcity bonus uses.
func (ps pickShares) share(b models.Bet) float64 {
	total := ps.total[b.MatchID]
	if total < scoring.ScarcityQuorum {
		return -1
	}
	return float64(ps.result[b.MatchID][scoring.Result(b)]) / float64(total)
}
