package ai

import (
	"context"
	"testing"
	"time"

	"bethoven/internal/db"
	"bethoven/internal/models"
)

// newSeedStore spins up a temp SQLite with an active tournament and returns the
// store + tournament id.
func newSeedStore(t *testing.T) (*db.Store, int64) {
	t.Helper()
	conn, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	store := db.NewStore(conn)
	tid, err := store.CreateTournament("Test Cup", true, ref)
	if err != nil {
		t.Fatalf("create tournament: %v", err)
	}
	return store, tid
}

// TestSeedPastGames seeds only already-started matches and is idempotent.
func TestSeedPastGames(t *testing.T) {
	store, tid := newSeedStore(t)
	u, err := store.CreateUser(Fingerprint, "BETanIA", models.RolePlayer, ref)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	mk := func(a, b string, starts time.Time) int64 {
		id, err := store.CreateMatch(models.Match{TournamentID: tid, TeamA: a, TeamB: b, Phase: models.PhaseGroup, StartsAt: starts})
		if err != nil {
			t.Fatalf("create match: %v", err)
		}
		return id
	}
	past1 := mk("A", "B", ref.Add(-48*time.Hour))
	past2 := mk("C", "D", ref.Add(-2*time.Hour))
	future := mk("E", "F", ref.Add(2*time.Hour))

	fp := &fakePredictor{p: Prediction{ScoreA: 2, ScoreB: 1, Rationale: "r", Confidence: "low"}}
	res, err := SeedPastGames(context.Background(), store, tid, fp, u.ID, ref, "", nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if res.Placed != 2 || res.AlreadyHad != 0 {
		t.Fatalf("first run: placed=%d alreadyHad=%d, want 2/0", res.Placed, res.AlreadyHad)
	}

	bets, err := store.BetsForUser(u.ID, tid)
	if err != nil {
		t.Fatalf("bets: %v", err)
	}
	got := map[int64]bool{}
	for _, b := range bets {
		got[b.MatchID] = true
	}
	if !got[past1] || !got[past2] {
		t.Errorf("past matches not seeded: %v", got)
	}
	if got[future] {
		t.Errorf("future match should NOT be seeded")
	}

	// Idempotent: a second run places nothing new.
	fp2 := &fakePredictor{p: Prediction{ScoreA: 0, ScoreB: 0}}
	res2, err := SeedPastGames(context.Background(), store, tid, fp2, u.ID, ref, "", nil)
	if err != nil {
		t.Fatalf("seed re-run: %v", err)
	}
	if res2.Placed != 0 || res2.AlreadyHad != 2 {
		t.Errorf("re-run: placed=%d alreadyHad=%d, want 0/2", res2.Placed, res2.AlreadyHad)
	}
	if fp2.calls != 0 {
		t.Errorf("re-run called predictor %d times, want 0 (all already bet)", fp2.calls)
	}
}
