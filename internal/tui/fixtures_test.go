package tui

import (
	"testing"
	"time"

	"bethoven/internal/models"
)

func TestCurrentMatchIndex(t *testing.T) {
	base := time.Date(2026, 6, 11, 16, 0, 0, 0, time.UTC)
	at := func(days int) time.Time { return base.Add(time.Duration(days) * 24 * time.Hour) }

	fin := func(d int) models.Match { return models.Match{StartsAt: at(d), Finished: true} }
	up := func(d int) models.Match { return models.Match{StartsAt: at(d)} }
	live := func(d int) models.Match { return models.Match{StartsAt: at(d), Live: true} }

	cases := []struct {
		name string
		list []models.Match
		want int
	}{
		{"empty", nil, 0},
		{"all upcoming → first", []models.Match{up(1), up(2), up(3)}, 0},
		{"finished then upcoming → first upcoming", []models.Match{fin(0), fin(1), up(2), up(3)}, 2},
		{"live in the middle → live", []models.Match{fin(0), fin(1), live(2), up(3)}, 2},
		{"live wins over earlier unsettled game", []models.Match{fin(0), up(1) /*unsettled past, not live*/, live(2), up(3)}, 2},
		{"all finished → last", []models.Match{fin(0), fin(1), fin(2)}, 2},
		{"single upcoming", []models.Match{up(1)}, 0},
	}
	for _, tc := range cases {
		if got := currentMatchIndex(tc.list); got != tc.want {
			t.Errorf("%s: currentMatchIndex = %d, want %d", tc.name, got, tc.want)
		}
	}
}
