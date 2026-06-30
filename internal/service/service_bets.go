package service

import (
	"errors"
	"fmt"

	"bethoven/internal/db"
	"bethoven/internal/models"
)

// Betting errors surfaced to the TUI.
var (
	ErrMatchNotFound = errors.New("match not found")
	ErrMatchLocked   = errors.New("betting closed: match has kicked off")
	ErrInvalidScore  = errors.New("scores must be between 0 and 99")
	// ErrPenaltiesNotApplicable: penalties only resolve a knockout tie that
	// finished level at 90'. ErrPenaltiesTied: a shootout has a winner, so the
	// two penalty totals must differ.
	ErrPenaltiesNotApplicable = errors.New("penalties apply only to a knockout tie drawn at 90'")
	ErrPenaltiesTied          = errors.New("penalty scores must differ — a shootout has a winner")
)

// Match returns a single match by id (for rendering the bet form).
func (s *Service) Match(matchID int64) (*models.Match, error) {
	m, err := s.store.MatchByID(matchID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, ErrMatchNotFound
	}
	return m, err
}

// MyBet returns the user's existing bet on a match (for pre-filling the form),
// or nil if they haven't bet yet.
func (s *Service) MyBet(userID, matchID int64) (*models.Bet, error) {
	b, err := s.store.BetForUserMatch(userID, matchID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil
	}
	return b, err
}

// MyBets returns the user's bets in the active tournament keyed by match id, so
// the fixtures list can mark which games already have a pick.
func (s *Service) MyBets(userID int64) (map[int64]models.Bet, error) {
	bets, err := s.store.BetsForUser(userID, s.tournamentID)
	if err != nil {
		return nil, err
	}
	byMatch := make(map[int64]models.Bet, len(bets))
	for _, b := range bets {
		byMatch[b.MatchID] = b
	}
	return byMatch, nil
}

// PlaceBet creates or updates a user's prediction for a match. It enforces the
// kickoff lock using the injected clock — the single most important rule — so a
// bet is rejected the instant the match's start time has passed. The server
// clock is the only authority; nothing client-supplied is trusted.
func (s *Service) PlaceBet(userID, matchID, predA, predB int64) error {
	if predA < 0 || predA > 99 || predB < 0 || predB > 99 {
		return ErrInvalidScore
	}

	m, err := s.store.MatchByID(matchID)
	if errors.Is(err, db.ErrNotFound) {
		return ErrMatchNotFound
	}
	if err != nil {
		return err
	}

	// Invariant: you can only bet on a match that has neither started nor
	// finished. The kickoff time is the primary gate (covers ongoing + ended);
	// the finished check is a belt-and-suspenders guard against clock skew or a
	// result entered before the listed start time.
	if m.Finished || !s.clock.Now().UTC().Before(m.StartsAt.UTC()) {
		return ErrMatchLocked
	}

	if err := s.store.UpsertBet(models.Bet{
		UserID:  userID,
		MatchID: matchID,
		PredA:   int(predA),
		PredB:   int(predB),
	}, s.clock.Now().UTC()); err != nil {
		return fmt.Errorf("place bet: %w", err)
	}
	// Emit using only data already in hand (m was loaded for the lock; userID and
	// scores are args) — NO extra domain-DB read. The actor's name is resolved
	// later, at read time, on the admin's own session.
	s.trackByID(userID, EvBetPlaced, map[string]string{
		"match": fmt.Sprintf("%s-%s", m.TeamA, m.TeamB),
		"pred":  fmt.Sprintf("%d-%d", predA, predB),
	})
	return nil
}
