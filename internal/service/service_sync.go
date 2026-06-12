package service

import (
	"strings"
	"time"

	"bethoven/internal/models"
	"bethoven/internal/results"
)

// reconcileWindow bounds how far a feed match's kickoff may sit from a local
// fixture's start time when we link them by team names. Team identity is
// effectively unique per tournament, so this is just a sanity guard against
// matching a rescheduled rematch (it is NOT used once an external_ref is stored
// — that link is authoritative).
const reconcileWindow = 48 * time.Hour

// teamAliases maps a normalised feed team name to the normalised name BEThoven
// stores, covering the spots where the results feed and our fixtures disagree.
// Keys/values are already lower-cased with "&" expanded to "and" and whitespace
// collapsed (the form normTeam produces). Extend this from the poller's
// "unmatched" log if new mismatches surface (e.g. once knockout teams resolve).
var teamAliases = map[string]string{
	// Verified against the live football-data.org "WC" feed (2026-06-12): every
	// team whose feed spelling differs from fixtures.json. Left = feed, right = ours.
	"bosnia-herzegovina": "bosnia and herzegovina",
	"cape verde islands": "cape verde",
	"congo dr":           "dr congo",
	"czechia":            "czech republic",
	"united states":      "usa",
	// Defensive: well-known feed alternates for teams in this tournament, in case
	// the feed's spelling shifts (it currently matches ours for these).
	"korea republic": "south korea",
	"cote d'ivoire":  "ivory coast",
	"côte d'ivoire":  "ivory coast",
	"türkiye":        "turkey",
}

// ApplyFeedResults reconciles a batch of external feed matches against the
// active tournament's fixtures and records results for the ones that have
// finished. It is the system-initiated counterpart to the admin's EnterResult
// and is called only by the in-process poller — never exposed over SSH — so it
// takes no requester and is not admin-gated.
//
// Rules (see CLAUDE.md "Automatic results"):
//   - Only matches the feed reports as Finished are considered.
//   - A finished match is matched to a fixture first by stored external_ref,
//     else by team identity within reconcileWindow; the link is then persisted.
//   - We NEVER overwrite a fixture that is already finished. That single guard
//     gives idempotency AND "the admin's manual entry always wins": a re-poll of
//     a settled match, or a match the admin already entered, is a no-op.
//   - The feed score is re-oriented to the fixture's stored team order, so
//     score_a always belongs to team_a regardless of who the feed calls "home".
//   - Knockouts whose 90' score can't be derived (nil Reg90) are left for the
//     admin rather than scored with an extra-time result.
func (s *Service) ApplyFeedResults(feed []results.FeedMatch) (results.Report, error) {
	var rep results.Report

	matches, err := s.store.ListMatches(s.tournamentID)
	if err != nil {
		return rep, err
	}
	byRef := make(map[string]int, len(matches)) // external_ref -> index into matches
	for i, m := range matches {
		if m.ExternalRef != "" {
			byRef[m.ExternalRef] = i
		}
	}

	for _, fm := range feed {
		if !fm.Finished {
			continue // nothing to record for a match still to be played / in play
		}

		idx, swap, ok := locate(fm, matches, byRef)
		if !ok {
			rep.Unmatched = append(rep.Unmatched, feedLabel(fm))
			continue
		}
		m := &matches[idx]

		// First time we resolve a fixture by name, persist the link so future
		// polls re-find it instantly (and we don't re-use it for another match).
		if m.ExternalRef == "" {
			if err := s.store.SetExternalRef(m.ID, fm.ExternalRef); err != nil {
				return rep, err
			}
			m.ExternalRef = fm.ExternalRef
			byRef[fm.ExternalRef] = idx
		}

		if m.Finished {
			rep.Skipped++ // already settled (earlier poll or manual admin entry) — never overwrite
			continue
		}
		if fm.Reg90 == nil {
			rep.Skipped++ // final, but we can't derive the regulation 90' score
			continue
		}

		a, b := fm.Reg90.A, fm.Reg90.B
		if swap {
			a, b = b, a
		}
		if err := s.store.SetResult(m.ID, a, b); err != nil {
			return rep, err
		}
		m.Finished, m.ScoreA, m.ScoreB = true, &a, &b
		rep.Applied++
	}
	return rep, nil
}

// locate finds the fixture a feed match corresponds to. It prefers a stored
// external_ref (authoritative); failing that it matches by team identity plus a
// kickoff sanity window, but only against fixtures not already linked to some
// other feed match. swap reports whether the feed's home/away order is reversed
// relative to the fixture's team_a/team_b.
func locate(fm results.FeedMatch, matches []models.Match, byRef map[string]int) (idx int, swap bool, ok bool) {
	if i, found := byRef[fm.ExternalRef]; found {
		sw, _ := orient(fm, matches[i]) // ref is authoritative; default orientation if names drifted
		return i, sw, true
	}
	for i := range matches {
		if matches[i].ExternalRef != "" {
			continue // already linked to another feed match
		}
		if sw, teamsOK := orient(fm, matches[i]); teamsOK && withinWindow(fm.KickoffUTC, matches[i].StartsAt) {
			return i, sw, true
		}
	}
	return 0, false, false
}

// orient reports whether a feed match's teams match a fixture's, and whether the
// home/away order is swapped relative to team_a/team_b.
func orient(fm results.FeedMatch, m models.Match) (swap, ok bool) {
	h, a := normTeam(fm.HomeTeam), normTeam(fm.AwayTeam)
	ta, tb := normTeam(m.TeamA), normTeam(m.TeamB)
	switch {
	case h == ta && a == tb:
		return false, true
	case h == tb && a == ta:
		return true, true
	}
	return false, false
}

// withinWindow reports whether two kickoff times are close enough to be the same
// fixture (see reconcileWindow).
func withinWindow(a, b time.Time) bool {
	d := a.Sub(b)
	if d < 0 {
		d = -d
	}
	return d <= reconcileWindow
}

// normTeam canonicalises a team name for comparison: lower-cased, "&" expanded
// to "and", whitespace collapsed, then run through teamAliases.
func normTeam(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "&", "and")
	s = strings.Join(strings.Fields(s), " ")
	if alias, ok := teamAliases[s]; ok {
		return alias
	}
	return s
}

// feedLabel is a human-readable identifier for an unmatched feed match, for logs.
func feedLabel(fm results.FeedMatch) string {
	return fm.HomeTeam + " v " + fm.AwayTeam + " [" + fm.ExternalRef + "]"
}
