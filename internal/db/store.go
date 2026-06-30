package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"bethoven/internal/models"
)

// ErrNotFound is returned by lookups when no row matches.
var ErrNotFound = errors.New("not found")

// Store is the typed query layer over the SQLite connection.
type Store struct{ db *sql.DB }

// NewStore wraps an open *sql.DB.
func NewStore(conn *sql.DB) *Store { return &Store{db: conn} }

const rfc = time.RFC3339

// --- tournaments ---------------------------------------------------------

// CreateTournament inserts a tournament and returns its id.
func (s *Store) CreateTournament(name string, active bool, now time.Time) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO tournaments(name, active, created_at) VALUES(?,?,?)`,
		name, b2i(active), now.UTC().Format(rfc),
	)
	if err != nil {
		return 0, fmt.Errorf("create tournament: %w", err)
	}
	return res.LastInsertId()
}

// ActiveTournament returns the active tournament, or ErrNotFound if none.
func (s *Store) ActiveTournament() (*models.Tournament, error) {
	row := s.db.QueryRow(`SELECT id, name, active, created_at FROM tournaments WHERE active=1 LIMIT 1`)
	var t models.Tournament
	var active int
	var created string
	switch err := row.Scan(&t.ID, &t.Name, &active, &created); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("active tournament: %w", err)
	}
	t.Active = active != 0
	t.CreatedAt, _ = time.Parse(rfc, created)
	return &t, nil
}

// --- users ---------------------------------------------------------------

// UserByFingerprint returns the user owning the SSH key fingerprint, or
// ErrNotFound if the key is unknown.
func (s *Store) UserByFingerprint(fp string) (*models.User, error) {
	row := s.db.QueryRow(
		`SELECT id, fingerprint, display_name, role, created_at FROM users WHERE fingerprint=?`, fp)
	return scanUser(row)
}

// CreateUser binds a fingerprint to a display name and role.
func (s *Store) CreateUser(fp, name string, role models.Role, now time.Time) (*models.User, error) {
	res, err := s.db.Exec(
		`INSERT INTO users(fingerprint, display_name, role, created_at) VALUES(?,?,?,?)`,
		fp, name, string(role), now.UTC().Format(rfc),
	)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	id, _ := res.LastInsertId()
	return &models.User{ID: id, Fingerprint: fp, DisplayName: name, Role: role, CreatedAt: now.UTC()}, nil
}

// DisplayNameTaken reports whether a display name is already in use,
// case-insensitively (ASCII). Used to keep leaderboard identities distinct.
func (s *Store) DisplayNameTaken(name string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM users WHERE display_name = ? COLLATE NOCASE`, name).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("display name check: %w", err)
	}
	return n > 0, nil
}

// SetUserRole updates a user's role (used to auto-promote admins on login).
func (s *Store) SetUserRole(userID int64, role models.Role) error {
	_, err := s.db.Exec(`UPDATE users SET role=? WHERE id=?`, string(role), userID)
	if err != nil {
		return fmt.Errorf("set user role: %w", err)
	}
	return nil
}

func scanUser(row *sql.Row) (*models.User, error) {
	var u models.User
	var role, created string
	switch err := row.Scan(&u.ID, &u.Fingerprint, &u.DisplayName, &role, &created); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("scan user: %w", err)
	}
	u.Role = models.Role(role)
	u.CreatedAt, _ = time.Parse(rfc, created)
	return &u, nil
}

// --- matches -------------------------------------------------------------

// CreateMatch inserts a fixture and returns its id.
func (s *Store) CreateMatch(m models.Match) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO matches(tournament_id, team_a, team_b, phase, group_label, starts_at)
		 VALUES(?,?,?,?,?,?)`,
		m.TournamentID, m.TeamA, m.TeamB, string(m.Phase), m.GroupLabel, m.StartsAt.UTC().Format(rfc),
	)
	if err != nil {
		return 0, fmt.Errorf("create match: %w", err)
	}
	return res.LastInsertId()
}

// UpdateMatch overwrites a match's editable fields (teams, phase, group, kickoff)
// for admin corrections. The score/finished flag is left untouched — that's
// SetResult's job. Returns ErrNotFound when no row matches.
func (s *Store) UpdateMatch(m models.Match) error {
	res, err := s.db.Exec(
		`UPDATE matches SET team_a=?, team_b=?, phase=?, group_label=?, starts_at=? WHERE id=?`,
		m.TeamA, m.TeamB, string(m.Phase), m.GroupLabel, m.StartsAt.UTC().Format(rfc), m.ID,
	)
	if err != nil {
		return fmt.Errorf("update match: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteMatch removes a match and every bet on it, atomically, returning how many
// bets were deleted (so the caller can report it). Returns ErrNotFound when the
// match doesn't exist.
func (s *Store) DeleteMatch(id int64) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("delete match: %w", err)
	}
	defer tx.Rollback()
	br, err := tx.Exec(`DELETE FROM bets WHERE match_id=?`, id)
	if err != nil {
		return 0, fmt.Errorf("delete match bets: %w", err)
	}
	bets, _ := br.RowsAffected()
	mr, err := tx.Exec(`DELETE FROM matches WHERE id=?`, id)
	if err != nil {
		return 0, fmt.Errorf("delete match: %w", err)
	}
	if n, _ := mr.RowsAffected(); n == 0 {
		return 0, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("delete match commit: %w", err)
	}
	return int(bets), nil
}

// CountMatches returns how many matches a tournament has (used for idempotent seeding).
func (s *Store) CountMatches(tournamentID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM matches WHERE tournament_id=?`, tournamentID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count matches: %w", err)
	}
	return n, nil
}

// MatchByID returns a single match or ErrNotFound.
func (s *Store) MatchByID(id int64) (*models.Match, error) {
	row := s.db.QueryRow(
		`SELECT id, tournament_id, team_a, team_b, phase, group_label, starts_at, score_a, score_b, penalty_a, penalty_b, finished
		 FROM matches WHERE id=?`, id)
	return scanMatch(rowScanner{row})
}

// ListMatches returns a tournament's matches ordered by kickoff time.
func (s *Store) ListMatches(tournamentID int64) ([]models.Match, error) {
	rows, err := s.db.Query(
		`SELECT id, tournament_id, team_a, team_b, phase, group_label, starts_at, score_a, score_b, penalty_a, penalty_b, finished
		 FROM matches WHERE tournament_id=? ORDER BY starts_at, id`, tournamentID)
	if err != nil {
		return nil, fmt.Errorf("list matches: %w", err)
	}
	defer rows.Close()
	var out []models.Match
	for rows.Next() {
		m, err := scanMatch(rowsScanner{rows})
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// SetResult records a match's regulation result and marks it finished. It also
// clears any penalty-shootout score, so re-entering a result (e.g. correcting a
// draw to a decisive score) never leaves a stale shootout behind; the admin
// re-enters penalties via SetPenalties if the corrected result is still a draw.
func (s *Store) SetResult(matchID int64, scoreA, scoreB int) error {
	res, err := s.db.Exec(
		`UPDATE matches SET score_a=?, score_b=?, penalty_a=NULL, penalty_b=NULL, finished=1 WHERE id=?`,
		scoreA, scoreB, matchID)
	if err != nil {
		return fmt.Errorf("set result: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetPenalties records the penalty-shootout score for a knockout tie drawn at
// 90'. It leaves the 90' score and finished flag untouched — penalties decide
// advancement only, never the scored result. Returns ErrNotFound if no such match.
func (s *Store) SetPenalties(matchID int64, penA, penB int) error {
	res, err := s.db.Exec(
		`UPDATE matches SET penalty_a=?, penalty_b=? WHERE id=?`,
		penA, penB, matchID)
	if err != nil {
		return fmt.Errorf("set penalties: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetResultIfUnfinished records a result only when the match is not already
// finished, in a single atomic statement. Returns whether it wrote. Used by the
// live feed so an admin result set concurrently can never be clobbered.
func (s *Store) SetResultIfUnfinished(matchID int64, scoreA, scoreB int) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE matches SET score_a=?, score_b=?, finished=1 WHERE id=? AND finished=0`,
		scoreA, scoreB, matchID)
	if err != nil {
		return false, fmt.Errorf("set result if unfinished: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// scanner abstracts *sql.Row and *sql.Rows so scanMatch serves both.
type scanner interface{ Scan(dest ...any) error }
type rowScanner struct{ r *sql.Row }
type rowsScanner struct{ r *sql.Rows }

func (s rowScanner) Scan(d ...any) error  { return s.r.Scan(d...) }
func (s rowsScanner) Scan(d ...any) error { return s.r.Scan(d...) }

func scanMatch(sc scanner) (*models.Match, error) {
	var m models.Match
	var phase, group, starts string
	var sa, sb, pa, pb sql.NullInt64
	var finished int
	switch err := sc.Scan(&m.ID, &m.TournamentID, &m.TeamA, &m.TeamB, &phase, &group, &starts, &sa, &sb, &pa, &pb, &finished); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("scan match: %w", err)
	}
	m.Phase = models.Phase(phase)
	m.GroupLabel = group
	m.StartsAt, _ = time.Parse(rfc, starts)
	if sa.Valid {
		v := int(sa.Int64)
		m.ScoreA = &v
	}
	if sb.Valid {
		v := int(sb.Int64)
		m.ScoreB = &v
	}
	if pa.Valid {
		v := int(pa.Int64)
		m.PenA = &v
	}
	if pb.Valid {
		v := int(pb.Int64)
		m.PenB = &v
	}
	m.Finished = finished != 0
	return &m, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
