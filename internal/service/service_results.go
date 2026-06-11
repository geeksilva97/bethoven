package service

import (
	"errors"
	"sort"

	"bethoven/internal/db"
	"bethoven/internal/models"
	"bethoven/internal/scoring"
)

// MatchResult is one row of a player's "My results" view: a match, their bet on
// it (nil if none), and the points earned.
type MatchResult struct {
	Match  models.Match
	Bet    *models.Bet
	Points int
}

// Standing is one row of the leaderboard.
type Standing struct {
	User  models.User
	Total int
}

// MatchStanding is one row of a per-match ranking: a player, their bet on that
// match (nil if none), and the points it earned.
type MatchStanding struct {
	User   models.User
	Bet    *models.Bet
	Points int
}

// Fixtures lists the active tournament's matches in kickoff order (for the
// fixtures/betting screen).
func (s *Service) Fixtures() ([]models.Match, error) {
	return s.store.ListMatches(s.tournamentID)
}

// MyResults returns the player's per-match results plus their running total.
// Matches the player never bet on appear with a nil Bet and 0 points.
func (s *Service) MyResults(userID int64) ([]MatchResult, int, error) {
	matches, err := s.store.ListMatches(s.tournamentID)
	if err != nil {
		return nil, 0, err
	}
	bets, err := s.store.BetsForUser(userID, s.tournamentID)
	if err != nil {
		return nil, 0, err
	}
	byMatch := make(map[int64]models.Bet, len(bets))
	for _, b := range bets {
		byMatch[b.MatchID] = b
	}

	out := make([]MatchResult, 0, len(matches))
	total := 0
	for _, m := range matches {
		row := MatchResult{Match: m}
		if b, ok := byMatch[m.ID]; ok {
			bcopy := b
			row.Bet = &bcopy
			row.Points = scoring.Points(b, m)
			total += row.Points
		}
		out = append(out, row)
	}
	return out, total, nil
}

// MatchLeaderboard ranks every player by the points they earned on a single
// match — the "who nailed this game" view. Players who bet on the match are
// ranked by points (desc) then name; players who didn't bet are omitted. Note
// this exposes individual picks, so the TUI only shows it once the match has a
// result (by which point everything is locked anyway).
func (s *Service) MatchLeaderboard(matchID int64) (*models.Match, []MatchStanding, error) {
	m, err := s.store.MatchByID(matchID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil, ErrMatchNotFound
	}
	if err != nil {
		return nil, nil, err
	}

	bets, err := s.store.BetsForMatch(matchID)
	if err != nil {
		return nil, nil, err
	}
	if len(bets) == 0 {
		return m, nil, nil
	}

	userIDs := make([]int64, 0, len(bets))
	for _, b := range bets {
		userIDs = append(userIDs, b.UserID)
	}
	users, err := s.store.UsersByIDs(userIDs)
	if err != nil {
		return nil, nil, err
	}

	rows := make([]MatchStanding, 0, len(bets))
	for _, b := range bets {
		bcopy := b
		rows = append(rows, MatchStanding{
			User:   users[b.UserID],
			Bet:    &bcopy,
			Points: scoring.Points(b, *m),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Points != rows[j].Points {
			return rows[i].Points > rows[j].Points
		}
		return rows[i].User.DisplayName < rows[j].User.DisplayName
	})
	return m, rows, nil
}

// Leaderboard returns every player's total, sorted by points (desc) then name.
func (s *Service) Leaderboard() ([]Standing, error) {
	users, err := s.store.AllUsers()
	if err != nil {
		return nil, err
	}
	matches, err := s.store.ListMatches(s.tournamentID)
	if err != nil {
		return nil, err
	}
	matchByID := make(map[int64]models.Match, len(matches))
	for _, m := range matches {
		matchByID[m.ID] = m
	}
	bets, err := s.store.AllBets(s.tournamentID)
	if err != nil {
		return nil, err
	}
	totals := make(map[int64]int)
	for _, b := range bets {
		if m, ok := matchByID[b.MatchID]; ok {
			totals[b.UserID] += scoring.Points(b, m)
		}
	}

	standings := make([]Standing, 0, len(users))
	for _, u := range users {
		standings = append(standings, Standing{User: u, Total: totals[u.ID]})
	}
	sort.Slice(standings, func(i, j int) bool {
		if standings[i].Total != standings[j].Total {
			return standings[i].Total > standings[j].Total
		}
		return standings[i].User.DisplayName < standings[j].User.DisplayName
	})
	return standings, nil
}
