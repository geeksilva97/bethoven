package standings

import (
	"testing"
	"time"

	"bethoven/internal/models"
)

// fin builds a finished group-stage match with the given score.
func fin(group, a, b string, sa, sb int) models.Match {
	return models.Match{
		TeamA: a, TeamB: b,
		Phase:      models.PhaseGroup,
		GroupLabel: group,
		Finished:   true,
		ScoreA:     &sa,
		ScoreB:     &sb,
	}
}

// upcoming builds an unplayed group-stage match (no score yet).
func upcoming(group, a, b string) models.Match {
	return models.Match{
		TeamA: a, TeamB: b,
		Phase:      models.PhaseGroup,
		GroupLabel: group,
		StartsAt:   time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC),
	}
}

func findGroup(gs []Group, label string) Group {
	for _, g := range gs {
		if g.Label == label {
			return g
		}
	}
	return Group{}
}

func teamRank(g Group, team string) int {
	for _, r := range g.Rows {
		if r.Team == team {
			return r.Rank
		}
	}
	return -1
}

// A clean group with a strict points order, no ties.
func TestGroupStandings_PointsOrder(t *testing.T) {
	ms := []models.Match{
		fin("Group A", "Mexico", "South Africa", 2, 0),
		fin("Group A", "South Korea", "Czechia", 1, 1),
		fin("Group A", "Mexico", "South Korea", 1, 0),
		fin("Group A", "Czechia", "South Africa", 0, 0),
		fin("Group A", "Mexico", "Czechia", 3, 0),
		fin("Group A", "South Africa", "South Korea", 0, 2),
	}
	g := findGroup(GroupStandings(ms), "Group A")
	if len(g.Rows) != 4 {
		t.Fatalf("want 4 rows, got %d", len(g.Rows))
	}
	// Mexico 9, South Korea 4, Czechia 2, South Africa 1.
	wantOrder := []string{"Mexico", "South Korea", "Czechia", "South Africa"}
	for i, team := range wantOrder {
		if g.Rows[i].Team != team {
			t.Errorf("rank %d: want %s, got %s", i+1, team, g.Rows[i].Team)
		}
	}
	if mx := g.Rows[0]; mx.Pts != 9 || mx.Played != 3 || mx.GF != 6 || mx.GA != 0 {
		t.Errorf("Mexico row wrong: %+v", mx)
	}
	for _, r := range g.Rows {
		if r.Tied {
			t.Errorf("%s unexpectedly flagged tied", r.Team)
		}
	}
}

// All four teams should appear even before any match is played.
func TestGroupStandings_ListsTeamsBeforeKickoff(t *testing.T) {
	ms := []models.Match{
		upcoming("Group B", "Canada", "Bosnia"),
		upcoming("Group B", "Qatar", "Switzerland"),
		upcoming("Group B", "Canada", "Qatar"),
	}
	g := findGroup(GroupStandings(ms), "Group B")
	if len(g.Rows) != 4 {
		t.Fatalf("want 4 teams listed pre-kickoff, got %d", len(g.Rows))
	}
	for _, r := range g.Rows {
		if r.Played != 0 || r.Pts != 0 {
			t.Errorf("%s should have no record yet: %+v", r.Team, r)
		}
	}
}

// Two teams level on points are separated by their head-to-head result, NOT by
// overall goal difference.
func TestGroupStandings_HeadToHeadBeatsGoalDiff(t *testing.T) {
	// A and B both finish on 6 points. B has the better OVERALL goal difference,
	// but A beat B head-to-head, so A must rank above B.
	ms := []models.Match{
		fin("Group C", "A", "B", 1, 0),     // A wins the decider
		fin("Group C", "A", "Weak", 1, 0),  // A: +1 here
		fin("Group C", "B", "Weak", 5, 0),  // B: +5 here (better overall GD)
		fin("Group C", "A", "Mid", 0, 1),   // both lose once to keep them level on 6
		fin("Group C", "B", "Mid", 0, 1),
	}
	g := findGroup(GroupStandings(ms), "Group C")
	ra, rb := teamRank(g, "A"), teamRank(g, "B")
	if !(ra < rb) {
		t.Fatalf("head-to-head should put A above B; got A=%d B=%d\nrows=%+v", ra, rb, g.Rows)
	}
}

// A three-way tie on points resolved by the head-to-head mini-table.
func TestGroupStandings_ThreeWayHeadToHead(t *testing.T) {
	// X, Y, Z each beat the minnow and form a cycle-free H2H where X > Y > Z.
	ms := []models.Match{
		fin("Group D", "X", "Min", 1, 0),
		fin("Group D", "Y", "Min", 1, 0),
		fin("Group D", "Z", "Min", 1, 0),
		// H2H among X,Y,Z: X beats Y and Z; Y beats Z.
		fin("Group D", "X", "Y", 1, 0),
		fin("Group D", "X", "Z", 1, 0),
		fin("Group D", "Y", "Z", 1, 0),
	}
	g := findGroup(GroupStandings(ms), "Group D")
	// X 9 (top), then Y, Z by H2H, Min last.
	want := []string{"X", "Y", "Z", "Min"}
	for i, team := range want {
		if g.Rows[i].Team != team {
			t.Errorf("rank %d: want %s, got %s (rows=%+v)", i+1, team, g.Rows[i].Team, g.Rows)
		}
	}
}

// Two teams identical on every computable criterion are flagged Tied.
func TestGroupStandings_UnresolvableTieFlagged(t *testing.T) {
	// P and Q draw their head-to-head and have identical overall records.
	ms := []models.Match{
		fin("Group E", "P", "Q", 1, 1),
		fin("Group E", "P", "R", 2, 0),
		fin("Group E", "Q", "R", 2, 0),
	}
	g := findGroup(GroupStandings(ms), "Group E")
	var pTied, qTied bool
	for _, r := range g.Rows {
		if r.Team == "P" {
			pTied = r.Tied
		}
		if r.Team == "Q" {
			qTied = r.Tied
		}
	}
	if !pTied || !qTied {
		t.Errorf("P and Q should both be flagged tied; got P=%v Q=%v\nrows=%+v", pTied, qTied, g.Rows)
	}
}

// The best eight third-placed teams across groups are marked qualifying.
func TestThirdPlaceRace_TopEightQualify(t *testing.T) {
	// Build 12 groups; each group's third-placed team gets a distinct points/GD
	// so the cross-group order is unambiguous.
	var ms []models.Match
	labels := []string{}
	for i := 0; i < 12; i++ {
		label := "Group " + string(rune('A'+i))
		labels = append(labels, label)
		// 1st and 2nd clearly ahead; the THIRD team's strength varies by i so the
		// 12 thirds rank cleanly. Third beats the 4th by (12-i) goals.
		margin := 12 - i
		ms = append(ms,
			fin(label, "First"+label, "Fourth"+label, 5, 0),
			fin(label, "Second"+label, "Fourth"+label, 4, 0),
			fin(label, "Third"+label, "Fourth"+label, margin, 0), // third's only win
			fin(label, "First"+label, "Second"+label, 1, 0),
			fin(label, "First"+label, "Third"+label, 1, 0),
			fin(label, "Second"+label, "Third"+label, 1, 0),
		)
	}
	groups := GroupStandings(ms)
	if len(groups) != 12 {
		t.Fatalf("want 12 groups, got %d", len(groups))
	}
	thirds := ThirdPlaceRace(groups)
	if len(thirds) != 12 {
		t.Fatalf("want 12 third-placed teams, got %d", len(thirds))
	}
	qualifying := 0
	for _, tp := range thirds {
		if tp.Qualifies {
			qualifying++
		}
	}
	if qualifying != QualifyingThirds {
		t.Errorf("want %d qualifying thirds, got %d", QualifyingThirds, qualifying)
	}
	// The first eight (best GD) qualify; the last four do not.
	for i, tp := range thirds {
		want := i < QualifyingThirds
		if tp.Qualifies != want {
			t.Errorf("third rank %d (%s, GD %d): Qualifies=%v want %v", tp.Rank, tp.Team, tp.GD(), tp.Qualifies, want)
		}
	}
}

// Knockout matches must never leak into the group computation.
func TestGroupStandings_IgnoresKnockouts(t *testing.T) {
	sa, sb := 1, 0
	ms := []models.Match{
		fin("Group A", "Mexico", "South Africa", 2, 0),
		{TeamA: "Mexico", TeamB: "Brazil", Phase: models.PhaseRound32, Finished: true, ScoreA: &sa, ScoreB: &sb},
	}
	groups := GroupStandings(ms)
	if len(groups) != 1 {
		t.Fatalf("want 1 group (knockout excluded), got %d", len(groups))
	}
	g := groups[0]
	for _, r := range g.Rows {
		if r.Team == "Mexico" && r.Played != 1 {
			t.Errorf("Mexico should have 1 group game, got %d (knockout leaked?)", r.Played)
		}
		if r.Team == "Brazil" {
			t.Errorf("Brazil is a knockout opponent and must not appear in the group table")
		}
	}
}
