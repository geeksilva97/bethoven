package db

import (
	"encoding/json"
	"fmt"
	"time"

	"bethoven/internal/models"
)

// FixtureFile is the JSON shape of a seed file: a tournament name, its
// group-stage matches, and optional per-team recent-form baselines. Knockout
// matches are added later via the admin TUI.
type FixtureFile struct {
	Tournament string         `json:"tournament"`
	Matches    []FixtureMatch `json:"matches"`
	Teams      []TeamForm     `json:"teams"`
}

// FixtureMatch is one group-stage fixture. StartsAt is RFC3339 UTC.
type FixtureMatch struct {
	TeamA      string `json:"team_a"`
	TeamB      string `json:"team_b"`
	GroupLabel string `json:"group_label"`
	StartsAt   string `json:"starts_at"`
}

// TeamForm is a team's pre-tournament recent form: a string of W/D/L results,
// left = oldest → right = newest. Read-only reference data (not stored in the
// DB); the service merges live tournament results on top at read time.
type TeamForm struct {
	Name string `json:"name"`
	Form string `json:"form"`
}

// ParseTeamForms extracts the per-team form baselines from a seed file as a
// name→form map. Absent/empty teams section yields an empty map (not an error).
func ParseTeamForms(raw []byte) (map[string]string, error) {
	var ff FixtureFile
	if err := json.Unmarshal(raw, &ff); err != nil {
		return nil, fmt.Errorf("parse fixtures: %w", err)
	}
	forms := make(map[string]string, len(ff.Teams))
	for _, t := range ff.Teams {
		if t.Name != "" {
			forms[t.Name] = t.Form
		}
	}
	return forms, nil
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
