package cardimg

import (
	"bytes"
	"image/png"
	"os"
	"testing"

	"bethoven/internal/models"
	"bethoven/internal/service"
)

// TestRenderDecodes asserts Render produces a real, decodable PNG of the expected
// size for both a podium card (with narrative + picks) and a bare card.
func TestRenderDecodes(t *testing.T) {
	cases := []struct {
		name string
		card service.PlayerCard
	}{
		{
			name: "champion with everything",
			card: service.PlayerCard{
				User:           models.User{ID: 1, DisplayName: "Ada Lovelace"},
				FinalRank:      1,
				Medal:          1,
				Total:          47,
				ExactHits:      6,
				CorrectResults: 14,
				StartRank:      9,
				PeakRank:       1,
				BestStreak:     5,
				HitRate:        70,
				MatchesBet:     20,
				Trajectory: []service.CardPoint{
					{Label: "2026-06-12", Position: 9, Total: 3},
					{Label: "2026-06-19", Position: 4, Total: 18},
					{Label: "2026-06-26", Position: 1, Total: 47},
				},
				BestPick: &service.MatchResult{
					Match:  models.Match{TeamA: "Brazil", TeamB: "Spain", ScoreA: ptr(2), ScoreB: ptr(1)},
					Bet:    &models.Bet{PredA: 2, PredB: 1},
					Points: 3,
				},
				WorstPick: &service.MatchResult{
					Match: models.Match{TeamA: "Japan", TeamB: "Germany", ScoreA: ptr(0), ScoreB: ptr(4)},
					Bet:   &models.Bet{PredA: 3, PredB: 0},
				},
				Narrative: "You opened in the pack and clawed your way to the summit. The Brazil call was the turning point — nobody else saw a 2-1.",
			},
		},
		{
			name: "bare mid-table card",
			card: service.PlayerCard{
				User:      models.User{ID: 2, DisplayName: "Bob"},
				FinalRank: 12,
				Total:     11,
			},
		},
		{
			name: "absurdly long name is truncated, not overflowed",
			card: service.PlayerCard{
				User:      models.User{ID: 3, DisplayName: "Maximilian Alexander Wolfeschlegelsteinhausenbergerdorff"},
				FinalRank: 4,
				Total:     22,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := Render(tc.card)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if len(data) == 0 {
				t.Fatal("Render returned no bytes")
			}
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("decode PNG: %v", err)
			}
			if b := img.Bounds(); b.Dx() != width || b.Dy() != height {
				t.Fatalf("bounds = %dx%d, want %dx%d", b.Dx(), b.Dy(), width, height)
			}
			// Optional visual dump: CARDIMG_DUMP=<dir> go test writes each case's PNG
			// there for eyeballing. Off by default, never affects CI.
			if dir := os.Getenv("CARDIMG_DUMP"); dir != "" {
				_ = os.WriteFile(dir+"/"+tc.name+".png", data, 0o644)
			}
		})
	}
}

func ptr(n int) *int { return &n }
