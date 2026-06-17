package analytics

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

const rfc = time.RFC3339

// Store is the typed query layer over the analytics database.
type Store struct{ db *sql.DB }

// NewStore wraps an open analytics *sql.DB.
func NewStore(conn *sql.DB) *Store { return &Store{db: conn} }

// Insert appends one event. Props are marshaled to a JSON object; a nil map is
// stored as "{}". Times are normalized to RFC3339 UTC so they sort lexically.
func (s *Store) Insert(ev Event) error {
	props := "{}"
	if len(ev.Props) > 0 {
		b, err := json.Marshal(ev.Props)
		if err != nil {
			return fmt.Errorf("marshal props: %w", err)
		}
		props = string(b)
	}
	_, err := s.db.Exec(
		`INSERT INTO events(at, user_id, fingerprint, actor, name, props) VALUES(?,?,?,?,?,?)`,
		ev.At.UTC().Format(rfc), ev.UserID, ev.Fingerprint, ev.Actor, ev.Name, props,
	)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

// Overview computes the headline KPIs as of now (UTC). RegisteredPlayers is left
// zero — the service fills it from the domain user table.
func (s *Store) Overview(now time.Time) (Overview, error) {
	now = now.UTC()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	weekAgo := now.AddDate(0, 0, -7)

	var ov Overview
	q := func(dst *int, query string, args ...any) error {
		return s.db.QueryRow(query, args...).Scan(dst)
	}
	if err := q(&ov.TotalAccesses, `SELECT COUNT(*) FROM events WHERE name=?`, nameSession); err != nil {
		return ov, fmt.Errorf("overview accesses: %w", err)
	}
	if err := q(&ov.UniquePlayers,
		`SELECT COUNT(DISTINCT user_id) FROM events WHERE name=? AND user_id<>0`, nameSession); err != nil {
		return ov, fmt.Errorf("overview unique: %w", err)
	}
	if err := q(&ov.AccessesToday,
		`SELECT COUNT(*) FROM events WHERE name=? AND at>=?`, nameSession, startOfToday.Format(rfc)); err != nil {
		return ov, fmt.Errorf("overview today: %w", err)
	}
	if err := q(&ov.Accesses7d,
		`SELECT COUNT(*) FROM events WHERE name=? AND at>=?`, nameSession, weekAgo.Format(rfc)); err != nil {
		return ov, fmt.Errorf("overview 7d: %w", err)
	}
	if err := q(&ov.BetsPlaced, `SELECT COUNT(*) FROM events WHERE name=?`, nameBetPlaced); err != nil {
		return ov, fmt.Errorf("overview bets: %w", err)
	}
	if err := q(&ov.ActivePlayers,
		`SELECT COUNT(DISTINCT user_id) FROM events WHERE user_id<>0 AND at>=?`, weekAgo.Format(rfc)); err != nil {
		return ov, fmt.Errorf("overview active: %w", err)
	}
	return ov, nil
}

// Recent returns the most recent events, newest first.
func (s *Store) Recent(limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT at, user_id, fingerprint, actor, name, props FROM events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("recent events: %w", err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		ev, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// PerPlayer aggregates engagement per user, busiest first. The actor is the most
// recent name seen for that user in the log (the service may override it with
// the player's current display name).
func (s *Store) PerPlayer() ([]PlayerStat, error) {
	rows, err := s.db.Query(`
		SELECT e.user_id,
		       (SELECT actor FROM events e2 WHERE e2.user_id=e.user_id ORDER BY e2.id DESC LIMIT 1) AS actor,
		       SUM(CASE WHEN e.name=? THEN 1 ELSE 0 END) AS accesses,
		       SUM(CASE WHEN e.name=? THEN 1 ELSE 0 END) AS bets,
		       MAX(e.at) AS last_seen
		FROM events e
		GROUP BY e.user_id
		ORDER BY accesses DESC, last_seen DESC`, nameSession, nameBetPlaced)
	if err != nil {
		return nil, fmt.Errorf("per-player: %w", err)
	}
	defer rows.Close()
	var out []PlayerStat
	for rows.Next() {
		var ps PlayerStat
		var lastSeen string
		if err := rows.Scan(&ps.UserID, &ps.Actor, &ps.Accesses, &ps.Bets, &lastSeen); err != nil {
			return nil, fmt.Errorf("scan per-player: %w", err)
		}
		ps.LastSeen, _ = time.Parse(rfc, lastSeen)
		out = append(out, ps)
	}
	return out, rows.Err()
}

// Timeline returns access counts per UTC day for the last `days` days (ending
// today), oldest first. Days with no accesses are omitted.
func (s *Store) Timeline(now time.Time, days int) ([]Bucket, error) {
	if days <= 0 {
		days = 7
	}
	now = now.UTC()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	cutoff := startOfToday.AddDate(0, 0, -(days - 1))
	rows, err := s.db.Query(`
		SELECT date(at) AS day, COUNT(*) AS n
		FROM events WHERE name=? AND at>=?
		GROUP BY day ORDER BY day`, nameSession, cutoff.Format(rfc))
	if err != nil {
		return nil, fmt.Errorf("timeline: %w", err)
	}
	defer rows.Close()
	var out []Bucket
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.Day, &b.Count); err != nil {
			return nil, fmt.Errorf("scan timeline: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func scanEvent(rows *sql.Rows) (Event, error) {
	var ev Event
	var at, props string
	if err := rows.Scan(&at, &ev.UserID, &ev.Fingerprint, &ev.Actor, &ev.Name, &props); err != nil {
		return ev, fmt.Errorf("scan event: %w", err)
	}
	ev.At, _ = time.Parse(rfc, at)
	if props != "" && props != "{}" {
		_ = json.Unmarshal([]byte(props), &ev.Props)
	}
	return ev, nil
}
