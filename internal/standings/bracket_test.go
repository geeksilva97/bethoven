package standings

import "testing"

// allowedFor returns the allowed third-place group set for an R32 match number.
func allowedFor(matchNo int) map[string]bool {
	for _, d := range r32Schedule {
		for _, side := range []slotRef{d.a, d.b} {
			if side.kind == '3' && d.match == matchNo {
				al := map[string]bool{}
				for _, g := range side.thirdFrom {
					al[g] = true
				}
				return al
			}
		}
	}
	return nil
}

func TestProjectR32_Structure(t *testing.T) {
	// Twelve groups A–L, each with a clear 1st/2nd/3rd, and exactly eight thirds
	// flagged qualifying — the constraint matchThirds must satisfy.
	var groups []Group
	letters := "ABCDEFGHIJKL"
	for _, c := range letters {
		l := string(c)
		groups = append(groups, Group{
			Label: "Group " + l,
			Rows: []TeamRow{
				{Team: "W" + l, Rank: 1},
				{Team: "R" + l, Rank: 2},
				{Team: "T" + l, Rank: 3},
				{Team: "X" + l, Rank: 4},
			},
		})
	}
	// Qualify the thirds from groups A,C,D,F,G,H,J,K (the current-prod set).
	qualifying := map[string]bool{"A": true, "C": true, "D": true, "F": true, "G": true, "H": true, "J": true, "K": true}
	var thirds []ThirdPlace
	rank := 1
	for _, c := range letters {
		l := string(c)
		thirds = append(thirds, ThirdPlace{
			TeamRow:   TeamRow{Team: "T" + l, Rank: rank},
			Group:     "Group " + l,
			Qualifies: qualifying[l],
		})
		rank++
	}

	r32 := ProjectR32(groups, thirds)
	if len(r32) != 16 {
		t.Fatalf("want 16 R32 matches, got %d", len(r32))
	}

	thirdsSeen := 0
	for _, pm := range r32 {
		// Every slot must resolve to a concrete team with this complete data.
		if pm.HomeTeam == "" || pm.AwayTeam == "" {
			t.Errorf("match %d has an unresolved slot: %+v", pm.Match, pm)
		}
		// A third must come from an allowed group for its slot.
		if al := allowedFor(pm.Match); al != nil {
			thirdsSeen++
			// The away side is always the third in the schedule's third-slots.
			g := groupLetter(pm.AwayDesc) // "3rd C" -> "C"
			if !al[g] {
				t.Errorf("match %d: third from group %s not allowed (allowed=%v)", pm.Match, g, al)
			}
			// No team meets a side from its own group: away third's group != home winner's group.
			homeG := groupLetter(pm.HomeDesc) // "Winner E" -> "E"
			if g == homeG {
				t.Errorf("match %d: third group %s equals winner group %s (same-group clash)", pm.Match, g, homeG)
			}
		}
	}
	if thirdsSeen != 8 {
		t.Errorf("want 8 third-slots, got %d", thirdsSeen)
	}
}
