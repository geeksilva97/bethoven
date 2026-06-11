package service

import (
	"errors"
	"sort"
	"time"

	"bethoven/internal/models"
	"bethoven/internal/scoring"
)

// ErrForbidden is returned when a non-admin invokes an admin-only operation.
// It is enforced here in the service, not just hidden in the UI.
var ErrForbidden = errors.New("forbidden: admin only")

// BetCell is one square of the admin grid: a player's bet on a match (nil if
// they didn't bet) and the points it earned.
type BetCell struct {
	Bet    *models.Bet
	Points int
}

// AllBetsGrid is the admin's master view: every player's pick and points across
// every match, plus per-player totals.
type AllBetsGrid struct {
	Matches []models.Match
	Users   []models.User
	Cells   map[int64]map[int64]BetCell // [matchID][userID]
	Totals  map[int64]int               // [userID]
}

func requireAdmin(u *models.User) error {
	if u == nil || u.Role != models.RoleAdmin {
		return ErrForbidden
	}
	return nil
}

// AddMatch adds a fixture to the active tournament (used for knockout rounds and
// fixes). Admin only.
func (s *Service) AddMatch(by *models.User, teamA, teamB string, phase models.Phase, groupLabel string, startsAt time.Time) (int64, error) {
	if err := requireAdmin(by); err != nil {
		return 0, err
	}
	return s.store.CreateMatch(models.Match{
		TournamentID: s.tournamentID,
		TeamA:        teamA,
		TeamB:        teamB,
		Phase:        phase,
		GroupLabel:   groupLabel,
		StartsAt:     startsAt,
	})
}

// EnterResult records a match's regulation result. Admin only.
func (s *Service) EnterResult(by *models.User, matchID int64, scoreA, scoreB int) error {
	if err := requireAdmin(by); err != nil {
		return err
	}
	if scoreA < 0 || scoreA > 99 || scoreB < 0 || scoreB > 99 {
		return ErrInvalidScore
	}
	if err := s.store.SetResult(matchID, scoreA, scoreB); err != nil {
		return ErrMatchNotFound
	}
	return nil
}

// AllBets builds the admin grid of every player's bets and points. Admin only.
func (s *Service) AllBets(by *models.User) (*AllBetsGrid, error) {
	if err := requireAdmin(by); err != nil {
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
	users, err := s.store.AllUsers()
	if err != nil {
		return nil, err
	}
	bets, err := s.store.AllBets(s.tournamentID)
	if err != nil {
		return nil, err
	}

	grid := &AllBetsGrid{
		Matches: matches,
		Users:   users,
		Cells:   make(map[int64]map[int64]BetCell),
		Totals:  make(map[int64]int),
	}
	for _, b := range bets {
		m, ok := matchByID[b.MatchID]
		if !ok {
			continue
		}
		pts := scoring.Points(b, m)
		if grid.Cells[b.MatchID] == nil {
			grid.Cells[b.MatchID] = make(map[int64]BetCell)
		}
		bcopy := b
		grid.Cells[b.MatchID][b.UserID] = BetCell{Bet: &bcopy, Points: pts}
		grid.Totals[b.UserID] += pts
	}
	// Stable ordering for display.
	sort.Slice(grid.Users, func(i, j int) bool {
		return grid.Users[i].DisplayName < grid.Users[j].DisplayName
	})
	return grid, nil
}
