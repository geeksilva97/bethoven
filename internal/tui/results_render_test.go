package tui

import (
	"strings"
	"testing"

	"bethoven/internal/models"
	"bethoven/internal/service"
)

// TestLeaderboardInlinePicksMultiMatch guards the fix for the overflow bug: with
// two simultaneous live matches and picks revealed, the board must fold each
// player's picks into inline columns on their own row (one row per player) instead
// of stacking a full per-match pick block per match — which pushed the standings
// off the top of the terminal. It checks the column headers render, each player
// appears once, and the frame stays comfortably within a normal terminal height.
func TestLeaderboardInlinePicksMultiMatch(t *testing.T) {
	std := func(id int64, name string, total, live int) service.Standing {
		return service.Standing{User: models.User{ID: id, DisplayName: name}, Total: total, LivePoints: live}
	}
	m := Model{width: 100, height: 40, revealLivePicks: true}
	for i := int64(1); i <= 16; i++ {
		m.standings = append(m.standings, std(i, "player"+string(rune('a'+i-1)), int(100-i), 0))
	}
	m.liveMatches = []models.Match{
		{ID: 10, TeamA: "Norway", TeamB: "Senegal", Live: true, LiveClock: "13'"},
		{ID: 11, TeamA: "Brazil", TeamB: "Argentina", Live: true, LiveClock: "22'"},
	}
	m.livePicks = []service.LiveMatchPicks{
		{Match: m.liveMatches[0], Picks: []service.LivePick{
			{User: models.User{ID: 1}, Bet: models.Bet{PredA: 2, PredB: 1}},
		}},
		{Match: m.liveMatches[1], Picks: []service.LivePick{
			{User: models.User{ID: 1}, Bet: models.Bet{PredA: 3, PredB: 1}},
		}},
	}

	frame := m.viewLeaderboard()

	for _, want := range []string{"NOR-SEN", "BRA-ARG"} {
		if !strings.Contains(frame, want) {
			t.Errorf("expected column header %q in frame:\n%s", want, frame)
		}
	}
	// One row per player: a name must not be repeated (the old per-match block
	// listed every player once per live match).
	if got := strings.Count(frame, "playera "); got != 1 {
		t.Errorf("player should appear on exactly one row, got %d:\n%s", got, frame)
	}
	// The whole board must fit a 40-row terminal (the bug overflowed it).
	if got := lineCount(frame); got > m.height {
		t.Errorf("frame is %d rows > terminal height %d (would scroll the top off):\n%s", got, m.height, frame)
	}
}
