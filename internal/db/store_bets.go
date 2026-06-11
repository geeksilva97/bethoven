package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"bethoven/internal/models"
)

// UpsertBet creates or replaces a user's prediction for a match. The caller
// (service) is responsible for enforcing the kickoff lock before calling this.
func (s *Store) UpsertBet(b models.Bet, now time.Time) error {
	ts := now.UTC().Format(rfc)
	_, err := s.db.Exec(
		`INSERT INTO bets(user_id, match_id, pred_a, pred_b, bonus_over, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?)
		 ON CONFLICT(user_id, match_id) DO UPDATE SET
		   pred_a=excluded.pred_a,
		   pred_b=excluded.pred_b,
		   bonus_over=excluded.bonus_over,
		   updated_at=excluded.updated_at`,
		b.UserID, b.MatchID, b.PredA, b.PredB, b2i(b.BonusOver), ts, ts,
	)
	if err != nil {
		return fmt.Errorf("upsert bet: %w", err)
	}
	return nil
}

// BetForUserMatch returns a user's bet on a match, or ErrNotFound.
func (s *Store) BetForUserMatch(userID, matchID int64) (*models.Bet, error) {
	row := s.db.QueryRow(
		`SELECT id, user_id, match_id, pred_a, pred_b, bonus_over, created_at, updated_at
		 FROM bets WHERE user_id=? AND match_id=?`, userID, matchID)
	return scanBet(rowScanner{row})
}

// BetsForUser returns every bet a user has placed in the given tournament.
func (s *Store) BetsForUser(userID, tournamentID int64) ([]models.Bet, error) {
	return s.queryBets(
		`SELECT b.id, b.user_id, b.match_id, b.pred_a, b.pred_b, b.bonus_over, b.created_at, b.updated_at
		 FROM bets b JOIN matches m ON m.id=b.match_id
		 WHERE b.user_id=? AND m.tournament_id=?`, userID, tournamentID)
}

// AllBets returns every bet in a tournament (admin-only path).
func (s *Store) AllBets(tournamentID int64) ([]models.Bet, error) {
	return s.queryBets(
		`SELECT b.id, b.user_id, b.match_id, b.pred_a, b.pred_b, b.bonus_over, b.created_at, b.updated_at
		 FROM bets b JOIN matches m ON m.id=b.match_id
		 WHERE m.tournament_id=?`, tournamentID)
}

// BetsForMatch returns every bet placed on a single match.
func (s *Store) BetsForMatch(matchID int64) ([]models.Bet, error) {
	return s.queryBets(
		`SELECT id, user_id, match_id, pred_a, pred_b, bonus_over, created_at, updated_at
		 FROM bets WHERE match_id=?`, matchID)
}

func (s *Store) queryBets(query string, args ...any) ([]models.Bet, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query bets: %w", err)
	}
	defer rows.Close()
	var out []models.Bet
	for rows.Next() {
		b, err := scanBet(rowsScanner{rows})
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

func scanBet(sc scanner) (*models.Bet, error) {
	var b models.Bet
	var bonus int
	var created, updated string
	switch err := sc.Scan(&b.ID, &b.UserID, &b.MatchID, &b.PredA, &b.PredB, &bonus, &created, &updated); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("scan bet: %w", err)
	}
	b.BonusOver = bonus != 0
	b.CreatedAt, _ = time.Parse(rfc, created)
	b.UpdatedAt, _ = time.Parse(rfc, updated)
	return &b, nil
}

// UsersByIDs returns the users for the given ids, keyed by id (for building
// the admin grid and leaderboard without N+1 lookups).
func (s *Store) UsersByIDs(ids []int64) (map[int64]models.User, error) {
	out := make(map[int64]models.User, len(ids))
	for _, id := range ids {
		row := s.db.QueryRow(
			`SELECT id, fingerprint, display_name, role, created_at FROM users WHERE id=?`, id)
		u, err := scanUser(row)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, err
		}
		out[id] = *u
	}
	return out, nil
}

// AllUsers returns every registered user (for the leaderboard).
func (s *Store) AllUsers() ([]models.User, error) {
	rows, err := s.db.Query(`SELECT id, fingerprint, display_name, role, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("all users: %w", err)
	}
	defer rows.Close()
	var out []models.User
	for rows.Next() {
		var u models.User
		var role, created string
		if err := rows.Scan(&u.ID, &u.Fingerprint, &u.DisplayName, &role, &created); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		u.Role = models.Role(role)
		u.CreatedAt, _ = time.Parse(rfc, created)
		out = append(out, u)
	}
	return out, rows.Err()
}
