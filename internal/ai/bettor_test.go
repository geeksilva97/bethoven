package ai

import (
	"context"
	"errors"
	"testing"
	"time"

	"bethoven/internal/models"
)

// fakePredictor returns a fixed prediction (or error) and counts calls.
type fakePredictor struct {
	p     Prediction
	err   error
	calls int
}

func (f *fakePredictor) Predict(_ context.Context, _ models.Match) (Prediction, error) {
	f.calls++
	return f.p, f.err
}

var ref = time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)

// TestBettorBetsOnlyOpenUnbet is the live fair-play invariant: the worker bets a
// match only when it's upcoming (not started, not finished) and unbet.
func TestBettorBetsOnlyOpenUnbet(t *testing.T) {
	matches := []models.Match{
		{ID: 1, TeamA: "A", TeamB: "B", StartsAt: ref.Add(2 * time.Hour)},                   // upcoming, unbet -> BET
		{ID: 2, TeamA: "C", TeamB: "D", StartsAt: ref.Add(-2 * time.Hour)},                  // already kicked off -> skip
		{ID: 3, TeamA: "E", TeamB: "F", StartsAt: ref.Add(3 * time.Hour), Finished: true},   // finished -> skip
		{ID: 4, TeamA: "G", TeamB: "H", StartsAt: ref.Add(4 * time.Hour)},                   // upcoming but already bet -> skip
	}
	myBets := map[int64]models.Bet{4: {MatchID: 4}}

	var placed []int64
	deps := Deps{
		Fixtures: func() ([]models.Match, error) { return matches, nil },
		MyBets:   func(int64) (map[int64]models.Bet, error) { return myBets, nil },
		PlaceBet: func(_, matchID, _, _ int64) error { placed = append(placed, matchID); return nil },
		Now:      func() time.Time { return ref },
	}
	mon := NewMonitor("test", time.Hour)
	fp := &fakePredictor{p: Prediction{ScoreA: 2, ScoreB: 1, Rationale: "reason", Confidence: "high"}}

	NewBettor(deps, fp, mon, 7, time.Hour, "", 0, 0).bet(context.Background())

	if len(placed) != 1 || placed[0] != 1 {
		t.Fatalf("expected only match 1 to be bet, got %v", placed)
	}
	if fp.calls != 1 {
		t.Errorf("predictor called %d times, want 1 (only the open unbet match)", fp.calls)
	}
	st := mon.Status()
	if st.Placed != 1 {
		t.Errorf("placed=%d, want 1", st.Placed)
	}
	if st.Skipped != 3 {
		t.Errorf("skipped=%d, want 3", st.Skipped)
	}
	if st.LastRun != ref || st.NextRun != ref.Add(time.Hour) {
		t.Errorf("run stamps wrong: last=%v next=%v", st.LastRun, st.NextRun)
	}
}

// TestBettorMaxPerRun caps how many bets a single pass places.
func TestBettorMaxPerRun(t *testing.T) {
	matches := []models.Match{
		{ID: 1, TeamA: "A", TeamB: "B", StartsAt: ref.Add(1 * time.Hour)},
		{ID: 2, TeamA: "C", TeamB: "D", StartsAt: ref.Add(2 * time.Hour)},
		{ID: 3, TeamA: "E", TeamB: "F", StartsAt: ref.Add(3 * time.Hour)},
	}
	var placed []int64
	deps := Deps{
		Fixtures: func() ([]models.Match, error) { return matches, nil },
		MyBets:   func(int64) (map[int64]models.Bet, error) { return map[int64]models.Bet{}, nil },
		PlaceBet: func(_, matchID, _, _ int64) error { placed = append(placed, matchID); return nil },
		Now:      func() time.Time { return ref },
	}
	mon := NewMonitor("test", time.Hour)
	fp := &fakePredictor{p: Prediction{ScoreA: 1, ScoreB: 0}}

	NewBettor(deps, fp, mon, 7, time.Hour, "", 2, 0).bet(context.Background())

	if len(placed) != 2 {
		t.Fatalf("maxPerRun=2 but placed %d bets (%v)", len(placed), placed)
	}
}

// TestBettorLockedRace records a mid-pass kickoff as "locked", not an error.
func TestBettorLockedRace(t *testing.T) {
	matches := []models.Match{{ID: 1, TeamA: "A", TeamB: "B", StartsAt: ref.Add(time.Minute)}}
	deps := Deps{
		Fixtures: func() ([]models.Match, error) { return matches, nil },
		MyBets:   func(int64) (map[int64]models.Bet, error) { return map[int64]models.Bet{}, nil },
		PlaceBet: func(_, _, _, _ int64) error { return errors.New("betting closed: match has kicked off") },
		Now:      func() time.Time { return ref },
	}
	mon := NewMonitor("test", time.Hour)
	NewBettor(deps, &fakePredictor{}, mon, 7, time.Hour, "", 0, 0).bet(context.Background())

	st := mon.Status()
	if st.Locked != 1 || st.Errored != 0 {
		t.Errorf("expected locked=1 errored=0, got locked=%d errored=%d", st.Locked, st.Errored)
	}
}

// TestBettorLookahead is the horizon invariant: with a lookahead window the worker
// bets only matches kicking off inside it, never a game further out — even though
// that game is upcoming and unbet.
func TestBettorLookahead(t *testing.T) {
	matches := []models.Match{
		{ID: 1, TeamA: "A", TeamB: "B", StartsAt: ref.Add(6 * time.Hour)},   // inside 48h window -> BET
		{ID: 2, TeamA: "C", TeamB: "D", StartsAt: ref.Add(40 * time.Hour)},  // inside 48h window -> BET
		{ID: 3, TeamA: "E", TeamB: "F", StartsAt: ref.Add(100 * time.Hour)}, // beyond window -> skip
		{ID: 4, TeamA: "G", TeamB: "H", StartsAt: ref.Add(200 * time.Hour)}, // beyond window -> skip
	}
	var placed []int64
	deps := Deps{
		Fixtures: func() ([]models.Match, error) { return matches, nil },
		MyBets:   func(int64) (map[int64]models.Bet, error) { return map[int64]models.Bet{}, nil },
		PlaceBet: func(_, matchID, _, _ int64) error { placed = append(placed, matchID); return nil },
		Now:      func() time.Time { return ref },
	}
	mon := NewMonitor("test", time.Hour)
	fp := &fakePredictor{p: Prediction{ScoreA: 1, ScoreB: 0}}

	NewBettor(deps, fp, mon, 7, time.Hour, "", 0, 48*time.Hour).bet(context.Background())

	if len(placed) != 2 || placed[0] != 1 || placed[1] != 2 {
		t.Fatalf("expected only the two in-window matches bet, got %v", placed)
	}
	if fp.calls != 2 {
		t.Errorf("predictor called %d times, want 2 (only the in-window matches)", fp.calls)
	}
}
