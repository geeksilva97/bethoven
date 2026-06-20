package db

import (
	"fmt"
	"time"

	"bethoven/internal/models"
)

// UpsertLeaderboardComment stores or replaces one player's current comment (the
// admin "regenerate this one" path). Keyed by user_id.
func (s *Store) UpsertLeaderboardComment(c models.LeaderboardComment) error {
	_, err := s.db.Exec(
		`INSERT INTO leaderboard_comments(user_id, player, text, created_at, expires_at)
		 VALUES(?,?,?,?,?)
		 ON CONFLICT(user_id) DO UPDATE SET
		   player=excluded.player,
		   text=excluded.text,
		   created_at=excluded.created_at,
		   expires_at=excluded.expires_at`,
		c.UserID, c.Player, c.Text, fmtTime(c.CreatedAt), fmtTime(c.ExpiresAt),
	)
	if err != nil {
		return fmt.Errorf("upsert leaderboard comment: %w", err)
	}
	return nil
}

// ReplaceLeaderboardComments swaps the whole set in one transaction (the worker's
// full pass): every previous row is dropped and the given set inserted, so a player
// who no longer has a comment leaves no stale row — mirroring ai.CommentCache.Replace.
func (s *Store) ReplaceLeaderboardComments(cs []models.LeaderboardComment) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("replace leaderboard comments: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit

	if _, err := tx.Exec(`DELETE FROM leaderboard_comments`); err != nil {
		return fmt.Errorf("replace leaderboard comments: clear: %w", err)
	}
	for _, c := range cs {
		if _, err := tx.Exec(
			`INSERT INTO leaderboard_comments(user_id, player, text, created_at, expires_at)
			 VALUES(?,?,?,?,?)`,
			c.UserID, c.Player, c.Text, fmtTime(c.CreatedAt), fmtTime(c.ExpiresAt),
		); err != nil {
			return fmt.Errorf("replace leaderboard comments: insert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("replace leaderboard comments: commit: %w", err)
	}
	return nil
}

// LeaderboardComments returns every stored comment (for restoring the cache at boot).
func (s *Store) LeaderboardComments() ([]models.LeaderboardComment, error) {
	rows, err := s.db.Query(
		`SELECT user_id, player, text, created_at, expires_at FROM leaderboard_comments`)
	if err != nil {
		return nil, fmt.Errorf("leaderboard comments: %w", err)
	}
	defer rows.Close()
	var out []models.LeaderboardComment
	for rows.Next() {
		var c models.LeaderboardComment
		var created, expires string
		if err := rows.Scan(&c.UserID, &c.Player, &c.Text, &created, &expires); err != nil {
			return nil, fmt.Errorf("scan leaderboard comment: %w", err)
		}
		c.CreatedAt = parseTime(created)
		c.ExpiresAt = parseTime(expires)
		out = append(out, c)
	}
	return out, rows.Err()
}

// fmtTime renders a timestamp as RFC3339 UTC text, or "" for the zero time (so a
// never-expires comment stores an empty expires_at, round-tripping back to zero).
func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(rfc)
}

// parseTime is the inverse of fmtTime: empty/garbage ⇒ the zero time.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(rfc, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
