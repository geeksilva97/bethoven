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

// PlaceBet creates or updates a user's prediction for a match. It enforces the
// kickoff lock using the injected clock — the single most important rule — so a
// bet is rejected the instant the match's start time has passed. The server
// clock is the only authority; nothing client-supplied is trusted.
func (s *Service) PlaceBet(userID, matchID, predA, predB int64, bonusOver bool) error {
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
		UserID:    userID,
		MatchID:   matchID,
		PredA:     int(predA),
		PredB:     int(predB),
		BonusOver: bonusOver,
	}, s.clock.Now().UTC()); err != nil {
		return fmt.Errorf("place bet: %w", err)
	}
	return nil
}
