package service

import (
	"testing"
	"time"

	"bethoven/internal/live"
	"bethoven/internal/models"
)

// TestLiveSituation builds the live-comment snapshot from an in-play match: it
// surfaces the score, clock, odds, the closest picks, and the players moving on
// provisional points — and reports nothing live when no feed is attached.
func TestLiveSituation(t *testing.T) {
	svc, store, fc := newTestService(t)
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")

	livem := addMatch(t, store, svc.tournamentID, base.Add(time.Hour))
	// Bets placed before kickoff. Alice 1-0 (exact at the live score), Bob 2-2 (off).
	_ = svc.PlaceBet(alice.ID, livem, 1, 0)
	_ = svc.PlaceBet(bob.ID, livem, 2, 2)

	// No live store ⇒ nothing live.
	if sit, isLive, err := svc.LiveSituation(); err != nil || isLive || len(sit.Matches) != 0 {
		t.Fatalf("no feed: want empty/!isLive, got isLive=%v matches=%d err=%v", isLive, len(sit.Matches), err)
	}

	fc.T = base.Add(90 * time.Minute) // past kickoff
	svc.SetLiveStore(liveSnap{livem: {
		A: 1, B: 0, State: live.StateIn, Clock: "30'", Odds: "TeamA -160 · O/U 2.5",
		Events: []models.MatchEvent{{Clock: "23'", Type: "Goal", Text: "Goal! TeamA 1, TeamB 0. Someone scores.", Scoring: true}},
	}})

	sit, isLive, err := svc.LiveSituation()
	if err != nil {
		t.Fatal(err)
	}
	if !isLive {
		t.Fatal("want isLive=true with an in-play match")
	}
	if len(sit.Matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(sit.Matches))
	}
	m := sit.Matches[0]
	if m.ScoreA != 1 || m.ScoreB != 0 || m.Clock != "30'" {
		t.Errorf("score/clock wrong: %+v", m)
	}
	if m.Odds != "TeamA -160 · O/U 2.5" {
		t.Errorf("odds = %q, want the snapshot odds", m.Odds)
	}
	// Key events flow through to the commenter so it can name the scorer.
	if len(m.Events) != 1 || m.Events[0].Clock != "23'" || m.Events[0].Type != "Goal" {
		t.Errorf("events = %+v, want one 23' Goal", m.Events)
	}
	if len(m.Picks) != 2 {
		t.Fatalf("picks = %d, want 2", len(m.Picks))
	}
	// Picks are sorted by provisional points desc: Alice (exact, leading) first.
	if m.Picks[0].Player != "Alice" || m.Picks[0].LivePoints == 0 {
		t.Errorf("top pick = %+v, want Alice with >0 live points", m.Picks[0])
	}
	if m.Picks[0].PredA != 1 || m.Picks[0].PredB != 0 {
		t.Errorf("top pick prediction = %d-%d, want 1-0", m.Picks[0].PredA, m.Picks[0].PredB)
	}
	// Alice should appear among the movers (she's gaining provisional points).
	var aliceMover bool
	for _, mv := range sit.Movers {
		if mv.Player == "Alice" {
			aliceMover = true
			if mv.LivePoints == 0 {
				t.Errorf("Alice mover has 0 live points: %+v", mv)
			}
		}
	}
	if !aliceMover {
		t.Error("Alice not among live movers")
	}
}
