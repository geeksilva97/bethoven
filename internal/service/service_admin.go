package service

import (
	"errors"
	"sort"
	"time"

	"bethoven/internal/models"
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

// AllBets builds the admin grid of every player's bets and points, across every
// match (including upcoming ones). Admin only.
func (s *Service) AllBets(by *models.User) (*AllBetsGrid, error) {
	if err := requireAdmin(by); err != nil {
		return nil, err
	}
	return s.buildBetsGrid(func(models.Match) bool { return true })
}

// PublicBetsGrid is the player-facing all-bets view, available only when an
// admin has enabled the public_bets setting (admins may always view it). To
// preserve blind betting it reveals a match's picks only once it has kicked off
// (or finished) — upcoming matches are omitted entirely, so picks are never
// exposed across the trust boundary before kickoff.
func (s *Service) PublicBetsGrid(by *models.User) (*AllBetsGrid, error) {
	if requireAdmin(by) != nil {
		enabled, err := s.PublicBetsEnabled()
		if err != nil {
			return nil, err
		}
		if !enabled {
			return nil, ErrForbidden
		}
	}
	return s.buildBetsGrid(func(m models.Match) bool {
		return m.Finished || !s.Now().Before(m.StartsAt)
	})
}

// buildBetsGrid assembles the bets grid for the active tournament, including
// only matches for which reveal returns true. Per-player totals are unaffected
// by hiding matches: a hidden match is always unstarted and so scores 0 points.
func (s *Service) buildBetsGrid(reveal func(models.Match) bool) (*AllBetsGrid, error) {
	all, err := s.store.ListMatches(s.tournamentID)
	if err != nil {
		return nil, err
	}
	matchByID := make(map[int64]models.Match, len(all))
	matches := make([]models.Match, 0, len(all))
	for _, m := range all {
		if !reveal(m) {
			continue
		}
		matchByID[m.ID] = m
		matches = append(matches, m)
	}
	users, err := s.store.AllUsers()
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

	grid := &AllBetsGrid{
		Matches: matches,
		Users:   users,
		Cells:   make(map[int64]map[int64]BetCell),
		Totals:  make(map[int64]int),
	}
	for _, b := range bets {
		m, ok := matchByID[b.MatchID]
		if !ok {
			continue // match hidden (not yet revealed) — skip its cells
		}
		pts := sc.points(b, m)
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
