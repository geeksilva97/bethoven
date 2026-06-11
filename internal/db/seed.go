package db

import (
	"encoding/json"
	"fmt"
	"time"

	"bethoven/internal/models"
)

// FixtureFile is the JSON shape of a seed file: a tournament name plus its
// group-stage matches. Knockout matches are added later via the admin TUI.
type FixtureFile struct {
	Tournament string         `json:"tournament"`
	Matches    []FixtureMatch `json:"matches"`
}

// FixtureMatch is one group-stage fixture. StartsAt is RFC3339 UTC.
type FixtureMatch struct {
	TeamA      string `json:"team_a"`
	TeamB      string `json:"team_b"`
	GroupLabel string `json:"group_label"`
	StartsAt   string `json:"starts_at"`
}

// EnsureSeeded guarantees an active tournament exists and, if it has no matches
// yet, imports the fixtures from raw JSON. It is idempotent: on a populated
// tournament it does nothing and reports seeded=false. Returns the active
// tournament id.
func (s *Store) EnsureSeeded(raw []byte, now time.Time) (tournamentID int64, seeded bool, err error) {
	var ff FixtureFile
	if err := json.Unmarshal(raw, &ff); err != nil {
		return 0, false, fmt.Errorf("parse fixtures: %w", err)
	}

	active, err := s.ActiveTournament()
	switch {
	case err == ErrNotFound:
		name := ff.Tournament
		if name == "" {
			name = "Tournament"
		}
		id, cerr := s.CreateTournament(name, true, now)
		if cerr != nil {
			return 0, false, cerr
		}
		tournamentID = id
	case err != nil:
		return 0, false, err
	default:
		tournamentID = active.ID
	}

	count, err := s.CountMatches(tournamentID)
	if err != nil {
		return 0, false, err
	}
	if count > 0 {
		return tournamentID, false, nil // already populated; no-op
	}

	for i, fm := range ff.Matches {
		starts, perr := time.Parse(time.RFC3339, fm.StartsAt)
		if perr != nil {
			return 0, false, fmt.Errorf("match %d (%s v %s): bad starts_at %q: %w",
				i, fm.TeamA, fm.TeamB, fm.StartsAt, perr)
		}
		_, cerr := s.CreateMatch(models.Match{
			TournamentID: tournamentID,
			TeamA:        fm.TeamA,
			TeamB:        fm.TeamB,
			Phase:        models.PhaseGroup,
			GroupLabel:   fm.GroupLabel,
			StartsAt:     starts,
		})
		if cerr != nil {
			return 0, false, cerr
		}
	}
	return tournamentID, true, nil
}
