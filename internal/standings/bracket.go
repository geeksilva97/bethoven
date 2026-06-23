package standings

import "strings"

// This file projects the Round of 32 from the current group tables — "if the
// group stage ended now, who plays whom". The 2026 bracket fixes every R32 slot
// by group position (matches 73–88): four winner-vs-runner-up ties, four
// runner-up-vs-runner-up ties, and eight winner-vs-third ties. Each third slot
// admits a third-placed team only from a specific set of groups; FIFA pre-solved
// all 495 combinations of which eight thirds qualify into one fixed bracket.
//
// We reproduce the fixed winner/runner-up slots exactly and assign the eight
// qualifying thirds to the eight third-slots by bipartite matching against those
// allowed sets (so no team ever meets a side from its own group). Because several
// valid assignments can satisfy the constraints, this is *a* correct bracket, not
// necessarily FIFA's specific official table — it is a projection, and the UI
// labels it as such.

// slotRef is one side of an R32 tie: a group winner, a group runner-up, or a
// third-placed team drawn from an allowed set of groups.
type slotRef struct {
	kind      byte     // 'W' winner, 'R' runner-up, '3' third
	group     string   // group letter for 'W'/'R'
	thirdFrom []string // allowed group letters for '3'
}

type r32Def struct {
	match int
	a, b  slotRef
}

func slotW(g string) slotRef          { return slotRef{kind: 'W', group: g} }
func slotR(g string) slotRef          { return slotRef{kind: 'R', group: g} }
func slot3(groups ...string) slotRef  { return slotRef{kind: '3', thirdFrom: groups} }

// r32Schedule is the fixed 2026 Round-of-32 slot allocation, matches 73–88.
var r32Schedule = []r32Def{
	{73, slotR("A"), slotR("B")},
	{74, slotW("E"), slot3("A", "B", "C", "D", "F")},
	{75, slotW("F"), slotR("C")},
	{76, slotW("C"), slotR("F")},
	{77, slotW("I"), slot3("C", "D", "F", "G", "H")},
	{78, slotR("E"), slotR("I")},
	{79, slotW("A"), slot3("C", "E", "F", "H", "I")},
	{80, slotW("L"), slot3("E", "H", "I", "J", "K")},
	{81, slotW("D"), slot3("B", "E", "F", "I", "J")},
	{82, slotW("G"), slot3("A", "E", "H", "I", "J")},
	{83, slotR("K"), slotR("L")},
	{84, slotW("H"), slotR("J")},
	{85, slotW("B"), slot3("E", "F", "G", "I", "J")},
	{86, slotW("J"), slotR("H")},
	{87, slotW("K"), slot3("D", "E", "I", "J", "L")},
	{88, slotR("D"), slotR("G")},
}

// bracketLeafOrder lists the R32 match numbers top-to-bottom in bracket position,
// derived from the fixed 2026 feed structure (R16 89–96 → QF 97–100 → SF 101–102
// → Final): 89=W74/W77, 90=W73/W75, 93=W83/W84, 94=W81/W82 feed the top half
// (SF 101); 91=W76/W78, 92=W79/W80, 95=W86/W88, 96=W85/W87 feed the bottom (SF
// 102). Because the tree is balanced, ordering the leaves this way lets a renderer
// merge adjacent pairs at every round without any further mapping.
var bracketLeafOrder = []int{74, 77, 73, 75, 83, 84, 81, 82, 76, 78, 79, 80, 86, 88, 85, 87}

// BracketLeaves returns the 16 projected R32 ties reordered top-to-bottom into
// their bracket positions, so a renderer can lay out the full R32→Final tree by
// merging neighbours. Any match missing from the projection is skipped.
func BracketLeaves(r32 []ProjMatch) []ProjMatch {
	byNum := make(map[int]ProjMatch, len(r32))
	for _, pm := range r32 {
		byNum[pm.Match] = pm
	}
	out := make([]ProjMatch, 0, len(bracketLeafOrder))
	for _, n := range bracketLeafOrder {
		if pm, ok := byNum[n]; ok {
			out = append(out, pm)
		}
	}
	return out
}

// ProjMatch is one projected Round-of-32 tie. A Team is "" when its slot cannot
// be filled yet (e.g. a third-slot left unmatched by incomplete data); Desc
// always carries the slot label ("Winner A", "Runner-up B", "3rd C").
type ProjMatch struct {
	Match    int
	HomeTeam string
	HomeDesc string
	AwayTeam string
	AwayDesc string
}

// ProjectR32 builds the projected Round of 32 from the current group tables and
// third-place race. Winners/runners-up come straight from each group's rank-1/2;
// the qualifying thirds are matched into the third-slots respecting the allowed
// group sets.
func ProjectR32(groups []Group, thirds []ThirdPlace) []ProjMatch {
	winner := map[string]string{}
	runner := map[string]string{}
	for _, g := range groups {
		letter := groupLetter(g.Label)
		for _, row := range g.Rows {
			switch row.Rank {
			case 1:
				winner[letter] = row.Team
			case 2:
				runner[letter] = row.Team
			}
		}
	}

	thirdTeam := map[string]string{} // group letter -> qualifying third's team
	qualGroups := []string{}
	for _, tp := range thirds {
		if tp.Qualifies {
			letter := groupLetter(tp.Group)
			thirdTeam[letter] = tp.Team
			qualGroups = append(qualGroups, letter)
		}
	}
	slotGroup := matchThirds(qualGroups) // match number -> assigned group letter

	resolve := func(ref slotRef, matchNo int) (team, desc string) {
		switch ref.kind {
		case 'W':
			return winner[ref.group], "Winner " + ref.group
		case 'R':
			return runner[ref.group], "Runner-up " + ref.group
		default: // '3'
			g, ok := slotGroup[matchNo]
			if !ok {
				return "", "3rd ?"
			}
			return thirdTeam[g], "3rd " + g
		}
	}

	out := make([]ProjMatch, 0, len(r32Schedule))
	for _, d := range r32Schedule {
		pm := ProjMatch{Match: d.match}
		pm.HomeTeam, pm.HomeDesc = resolve(d.a, d.match)
		pm.AwayTeam, pm.AwayDesc = resolve(d.b, d.match)
		out = append(out, pm)
	}
	return out
}

// matchThirds assigns qualifying third-place groups to the eight third-slots via
// Kuhn's bipartite-matching algorithm, honouring each slot's allowed-group set.
// Returns match-number → group letter. With the real 2026 schedule and eight
// valid thirds a perfect matching always exists; incomplete data may leave some
// slots unmatched (rendered "3rd ?").
func matchThirds(qualGroups []string) map[int]string {
	type slot struct {
		match   int
		allowed map[string]bool
	}
	var slots []slot
	for _, d := range r32Schedule {
		for _, side := range []slotRef{d.a, d.b} {
			if side.kind == '3' {
				al := make(map[string]bool, len(side.thirdFrom))
				for _, g := range side.thirdFrom {
					al[g] = true
				}
				slots = append(slots, slot{d.match, al})
			}
		}
	}

	groupToSlot := map[string]int{} // group letter -> slot index
	slotToGroup := map[int]string{} // slot index -> group letter
	var augment func(si int, seen map[string]bool) bool
	augment = func(si int, seen map[string]bool) bool {
		for _, g := range qualGroups {
			if !slots[si].allowed[g] || seen[g] {
				continue
			}
			seen[g] = true
			if cur, taken := groupToSlot[g]; !taken || augment(cur, seen) {
				groupToSlot[g] = si
				slotToGroup[si] = g
				return true
			}
		}
		return false
	}
	for si := range slots {
		augment(si, map[string]bool{})
	}

	res := make(map[int]string, len(slots))
	for si, g := range slotToGroup {
		res[slots[si].match] = g
	}
	return res
}

// groupLetter extracts the short label ("A" from "Group A"); falls back to the
// whole label when it has no trailing token.
func groupLetter(label string) string {
	if f := strings.Fields(label); len(f) > 0 {
		return f[len(f)-1]
	}
	return label
}
