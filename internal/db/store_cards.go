package db

import (
	"fmt"
	"time"

	"bethoven/internal/models"
)

// UpsertPlayerCard stores or replaces one player's end-of-tournament card
// narrative (the only persisted part of a card). Keyed by user_id, so re-generating
// overwrites in place. Mirrors UpsertLeaderboardComment.
func (s *Store) UpsertPlayerCard(userID int64, narrative string, at time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO player_cards(user_id, narrative, generated_at)
		 VALUES(?,?,?)
		 ON CONFLICT(user_id) DO UPDATE SET
		   narrative=excluded.narrative,
		   generated_at=excluded.generated_at`,
		userID, narrative, fmtTime(at),
	)
	if err != nil {
		return fmt.Errorf("upsert player card: %w", err)
	}
	return nil
}

// AllPlayerCards returns every stored card narrative, keyed by user id, so the
// service can overlay them onto the freshly computed cards at read time.
func (s *Store) AllPlayerCards() (map[int64]models.PlayerCardNarrative, error) {
	rows, err := s.db.Query(`SELECT user_id, narrative, generated_at FROM player_cards`)
	if err != nil {
		return nil, fmt.Errorf("player cards: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]models.PlayerCardNarrative)
	for rows.Next() {
		var uid int64
		var text, at string
		if err := rows.Scan(&uid, &text, &at); err != nil {
			return nil, fmt.Errorf("scan player card: %w", err)
		}
		out[uid] = models.PlayerCardNarrative{Text: text, At: parseTime(at)}
	}
	return out, rows.Err()
}
