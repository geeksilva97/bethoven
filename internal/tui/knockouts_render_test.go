package tui

import (
	"strings"
	"testing"
	"time"

	"bethoven/internal/models"
	"bethoven/internal/service"
	"bethoven/internal/standings"
)

func koTestPicture() service.KnockoutPicture {
	fin := func(a, b string, sa, sb int) models.Match {
		return models.Match{TeamA: a, TeamB: b, Phase: models.PhaseRound32, Finished: true, ScoreA: &sa, ScoreB: &sb}
	}
	return service.KnockoutPicture{
		Groups: []standings.Group{{
			Label: "Group A",
			Rows: []standings.TeamRow{
				{Team: "Mexico", Played: 3, GF: 6, GA: 0, Pts: 9, Rank: 1},
				{Team: "South Korea", Played: 3, GF: 3, GA: 2, Pts: 4, Rank: 2},
				{Team: "Czechia", Played: 3, GF: 1, GA: 2, Pts: 2, Rank: 3},
				{Team: "South Africa", Played: 3, GF: 0, GA: 6, Pts: 1, Rank: 4},
			},
		}},
		ThirdPlace: []standings.ThirdPlace{
			{TeamRow: standings.TeamRow{Team: "Czechia", Played: 3, GF: 1, GA: 2, Pts: 2, Rank: 1}, Group: "Group A", Qualifies: true},
		},
		Bracket: []service.BracketRound{
			{Phase: models.PhaseRound32, Label: "Round of 32", Matches: []models.Match{
				fin("Mexico", "Brazil", 2, 1),
				{TeamA: "USA", TeamB: "Italy", Phase: models.PhaseRound32, StartsAt: time.Date(2026, 6, 29, 18, 0, 0, 0, time.UTC)},
			}},
			{Phase: models.PhaseRound16, Label: "Round of 16"},
			{Phase: models.PhaseRound8, Label: "Quarter-final"},
			{Phase: models.PhaseSemi, Label: "Semi-final"},
			{Phase: models.PhaseFinal, Label: "Final"},
		},
	}
}

func TestViewKnockoutGroups(t *testing.T) {
	m := Model{width: 100, height: 40, ko: koTestPicture(), koView: koViewGroups}
	frame := m.viewKnockoutGroups()
	for _, want := range []string{"Group A", "Mexico", "Best 3rd-placed", "Czechia"} {
		if !strings.Contains(frame, want) {
			t.Errorf("group view missing %q\n---\n%s", want, frame)
		}
	}
}

func TestViewKnockoutBracket(t *testing.T) {
	m := Model{width: 100, height: 40, ko: koTestPicture(), koView: koViewBracket}
	frame := m.viewKnockoutBracket()
	for _, want := range []string{"Round of 32", "Mexico", "USA", "Round of 16", "not drawn yet"} {
		if !strings.Contains(frame, want) {
			t.Errorf("bracket view missing %q\n---\n%s", want, frame)
		}
	}
}

// The screen opens on the bracket once a knockout match exists, else on groups.
func TestBracketDrawn(t *testing.T) {
	if !bracketDrawn(koTestPicture()) {
		t.Error("picture with R32 matches should report bracket drawn")
	}
	empty := service.KnockoutPicture{Bracket: []service.BracketRound{{Phase: models.PhaseRound32, Label: "Round of 32"}}}
	if bracketDrawn(empty) {
		t.Error("picture with no entered matches should not report bracket drawn")
	}
}
