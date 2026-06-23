// Package standings computes World Cup group tables and the cross-group race for
// the best third-placed teams — the pure, DB-free core behind the "Knockouts"
// screen. Like internal/scoring it is a leaf package: it takes []models.Match and
// returns computed tables, with no clock, no DB, and no behaviour beyond the
// arithmetic, which keeps it table-testable.
//
// A match counts toward a table once it is Finished with a recorded score. To
// fold in-play matches provisionally (mirroring the leaderboard's live fold), the
// caller passes a synthetic copy whose live score is set as if final — this
// package never reads the Live* fields, so it stays pure.
package standings

import (
	"sort"

	"bethoven/internal/models"
)

// QualifyingThirds is how many of the twelve third-placed teams advance to the
// Round of 32 under the 48-team format.
const QualifyingThirds = 8

// TeamRow is one team's record. GD is GF−GA. Rank is the team's 1-based position
// in its group (in a Group) or, in a ThirdPlace, its cross-group third-place rank.
// Tied marks a row that could not be separated from an adjacent one by any
// criterion BEThoven can compute — points, head-to-head, then overall goal
// difference and goals scored. The remaining official tiebreakers (fair-play
// conduct score, then FIFA ranking) need data we don't track, so rather than
// guess, such rows are flagged for the UI to show as "pending official tiebreak".
type TeamRow struct {
	Team   string
	Played int
	Won    int
	Drawn  int
	Lost   int
	GF     int
	GA     int
	Pts    int
	Rank   int
	Tied   bool
}

// GD is goal difference.
func (r TeamRow) GD() int { return r.GF - r.GA }

// Group is a single group's table, rows sorted best-first.
type Group struct {
	Label string
	Rows  []TeamRow
}

// ThirdPlace is one group's third-placed team in the cross-group race. Rank is
// the 1-based rank among all third-placed teams; Qualifies marks the top eight.
type ThirdPlace struct {
	TeamRow
	Group     string
	Qualifies bool
}

// counted reports whether a match contributes to a group table: a group-stage
// fixture with a recorded score. (The caller substitutes a synthetic finished
// match to fold in-play scores, so this never inspects the Live* fields.)
func counted(m models.Match) bool {
	return m.Phase == models.PhaseGroup && m.GroupLabel != "" &&
		m.Finished && m.ScoreA != nil && m.ScoreB != nil
}

// GroupStandings builds every group's table from the supplied matches, groups
// ordered by label. All teams that appear in a group's fixtures are listed even
// before they have played, so the table is complete from the first kickoff.
func GroupStandings(matches []models.Match) []Group {
	labels := []string{}
	byLabel := map[string][]models.Match{}
	for _, m := range matches {
		if m.Phase != models.PhaseGroup || m.GroupLabel == "" {
			continue
		}
		if _, ok := byLabel[m.GroupLabel]; !ok {
			labels = append(labels, m.GroupLabel)
		}
		byLabel[m.GroupLabel] = append(byLabel[m.GroupLabel], m)
	}
	sort.Strings(labels)
	out := make([]Group, 0, len(labels))
	for _, label := range labels {
		out = append(out, buildGroup(label, byLabel[label]))
	}
	return out
}

// sortKey captures every separator we can compute, so adjacent rows that are
// equal on all of them can be flagged as an unresolvable tie. h2h* are the
// head-to-head sub-table values within the team's points-level run (0 for a team
// not tied with anyone on points).
type sortKey struct {
	pts, h2hPts, h2hGD, h2hGF, gd, gf int
}

func buildGroup(label string, ms []models.Match) Group {
	rows := map[string]*TeamRow{}
	order := []string{} // first-appearance order, for deterministic display of equals
	ensure := func(team string) *TeamRow {
		if _, ok := rows[team]; !ok {
			rows[team] = &TeamRow{Team: team}
			order = append(order, team)
		}
		return rows[team]
	}
	for _, m := range ms {
		ra, rb := ensure(m.TeamA), ensure(m.TeamB)
		if !counted(m) {
			continue
		}
		a, b := *m.ScoreA, *m.ScoreB
		ra.Played++
		rb.Played++
		ra.GF += a
		ra.GA += b
		rb.GF += b
		rb.GA += a
		switch {
		case a > b:
			ra.Won++
			rb.Lost++
			ra.Pts += 3
		case a < b:
			rb.Won++
			ra.Lost++
			rb.Pts += 3
		default:
			ra.Drawn++
			rb.Drawn++
			ra.Pts++
			rb.Pts++
		}
	}

	ordered := make([]*TeamRow, len(order))
	for i, t := range order {
		ordered[i] = rows[t]
	}

	// Default key: overall points/GD/goals, no head-to-head component.
	keys := make(map[string]sortKey, len(ordered))
	for _, r := range ordered {
		keys[r.Team] = sortKey{pts: r.Pts, gd: r.GD(), gf: r.GF}
	}

	// Phase 1: order by overall points (stable keeps first-appearance order for ties).
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Pts > ordered[j].Pts })

	// Phase 2: within each run of teams level on points, apply the head-to-head
	// mini-table (points, then GD, then goals among only those teams) before
	// falling back to overall GD and goals — the FIFA group tiebreak order.
	for i := 0; i < len(ordered); {
		j := i + 1
		for j < len(ordered) && ordered[j].Pts == ordered[i].Pts {
			j++
		}
		if j-i >= 2 {
			run := ordered[i:j]
			names := make([]string, len(run))
			for k, r := range run {
				names[k] = r.Team
			}
			h := headToHead(names, ms)
			for _, r := range run {
				hr := h[r.Team]
				keys[r.Team] = sortKey{r.Pts, hr.Pts, hr.GD(), hr.GF, r.GD(), r.GF}
			}
			sort.SliceStable(run, func(x, y int) bool {
				return lessKey(keys[run[x].Team], keys[run[y].Team])
			})
		}
		i = j
	}

	for idx, r := range ordered {
		r.Rank = idx + 1
	}
	// Flag adjacent rows we cannot separate by any computable criterion.
	for idx := 1; idx < len(ordered); idx++ {
		if keys[ordered[idx-1].Team] == keys[ordered[idx].Team] {
			ordered[idx-1].Tied = true
			ordered[idx].Tied = true
		}
	}

	out := make([]TeamRow, len(ordered))
	for i, r := range ordered {
		out[i] = *r
	}
	return Group{Label: label, Rows: out}
}

// lessKey reports whether key a ranks above key b (more points, then head-to-head
// points/GD/goals, then overall GD/goals).
func lessKey(a, b sortKey) bool {
	switch {
	case a.pts != b.pts:
		return a.pts > b.pts
	case a.h2hPts != b.h2hPts:
		return a.h2hPts > b.h2hPts
	case a.h2hGD != b.h2hGD:
		return a.h2hGD > b.h2hGD
	case a.h2hGF != b.h2hGF:
		return a.h2hGF > b.h2hGF
	case a.gd != b.gd:
		return a.gd > b.gd
	default:
		return a.gf > b.gf
	}
}

// headToHead builds the mini-table among exactly the named teams, counting only
// matches played between them.
func headToHead(names []string, ms []models.Match) map[string]TeamRow {
	inSet := make(map[string]bool, len(names))
	for _, n := range names {
		inSet[n] = true
	}
	h := make(map[string]*TeamRow, len(names))
	for _, n := range names {
		h[n] = &TeamRow{Team: n}
	}
	for _, m := range ms {
		if !counted(m) || !inSet[m.TeamA] || !inSet[m.TeamB] {
			continue
		}
		a, b := *m.ScoreA, *m.ScoreB
		ha, hb := h[m.TeamA], h[m.TeamB]
		ha.GF += a
		ha.GA += b
		hb.GF += b
		hb.GA += a
		switch {
		case a > b:
			ha.Pts += 3
		case a < b:
			hb.Pts += 3
		default:
			ha.Pts++
			hb.Pts++
		}
	}
	res := make(map[string]TeamRow, len(names))
	for k, v := range h {
		res[k] = *v
	}
	return res
}

// ThirdPlaceRace ranks every group's third-placed team against the others for the
// eight Round-of-32 spots. Because these teams come from different groups,
// head-to-head cannot apply: the order is points → overall GD → goals scored,
// then (unresolvable, flagged Tied) the official fair-play and FIFA-ranking steps
// we don't track. Group label breaks remaining ties only to stay deterministic.
func ThirdPlaceRace(groups []Group) []ThirdPlace {
	thirds := []ThirdPlace{}
	for _, g := range groups {
		for _, r := range g.Rows {
			if r.Rank == 3 {
				r.Rank = 0
				r.Tied = false // group-context tie flag doesn't carry over
				thirds = append(thirds, ThirdPlace{TeamRow: r, Group: g.Label})
				break
			}
		}
	}
	sort.SliceStable(thirds, func(i, j int) bool {
		a, b := thirds[i], thirds[j]
		switch {
		case a.Pts != b.Pts:
			return a.Pts > b.Pts
		case a.GD() != b.GD():
			return a.GD() > b.GD()
		case a.GF != b.GF:
			return a.GF > b.GF
		default:
			return a.Group < b.Group
		}
	})
	for i := range thirds {
		thirds[i].Rank = i + 1
		thirds[i].Qualifies = i < QualifyingThirds
	}
	for i := 1; i < len(thirds); i++ {
		a, b := thirds[i-1], thirds[i]
		if a.Pts == b.Pts && a.GD() == b.GD() && a.GF == b.GF {
			thirds[i-1].Tied = true
			thirds[i].Tied = true
		}
	}
	return thirds
}
