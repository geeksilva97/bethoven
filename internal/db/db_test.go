package db

import (
	"errors"
	"os"
	"testing"
	"time"

	"bethoven/internal/models"
)

// newTestStore opens a fresh on-disk SQLite in a temp dir (real driver, real
// schema) for each test.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	conn, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return NewStore(conn)
}

var testNow = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func TestEnsureSeededImportsFixtures(t *testing.T) {
	s := newTestStore(t)
	raw, err := os.ReadFile("testdata/fixtures.json")
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}

	tid, seeded, err := s.EnsureSeeded(raw, testNow)
	if err != nil {
		t.Fatalf("EnsureSeeded: %v", err)
	}
	if !seeded {
		t.Fatal("expected seeded=true on empty tournament")
	}

	n, err := s.CountMatches(tid)
	if err != nil {
		t.Fatalf("CountMatches: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 group matches, got %d", n)
	}

	tour, err := s.ActiveTournament()
	if err != nil {
		t.Fatalf("ActiveTournament: %v", err)
	}
	if tour.Name != "Test Cup" || !tour.Active {
		t.Errorf("unexpected tournament %+v", tour)
	}
}

func TestEnsureSeededIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	raw, _ := os.ReadFile("testdata/fixtures.json")

	tid1, seeded1, err := s.EnsureSeeded(raw, testNow)
	if err != nil || !seeded1 {
		t.Fatalf("first seed: seeded=%v err=%v", seeded1, err)
	}
	tid2, seeded2, err := s.EnsureSeeded(raw, testNow)
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if seeded2 {
		t.Error("expected seeded=false on populated tournament")
	}
	if tid1 != tid2 {
		t.Errorf("tournament id changed: %d -> %d", tid1, tid2)
	}
	if n, _ := s.CountMatches(tid1); n != 3 {
		t.Errorf("expected matches unchanged at 3, got %d", n)
	}
}

func TestUserRoundTrip(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.UserByFingerprint("SHA256:nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}

	u, err := s.CreateUser("SHA256:abc", "Antonio", models.RolePlayer, testNow)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	got, err := s.UserByFingerprint("SHA256:abc")
	if err != nil {
		t.Fatalf("UserByFingerprint: %v", err)
	}
	if got.ID != u.ID || got.DisplayName != "Antonio" || got.Role != models.RolePlayer {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestSetResultAndScores(t *testing.T) {
	s := newTestStore(t)
	tid, _ := s.CreateTournament("T", true, testNow)
	mid, err := s.CreateMatch(models.Match{
		TournamentID: tid, TeamA: "A", TeamB: "B",
		Phase: models.PhaseGroup, StartsAt: testNow,
	})
	if err != nil {
		t.Fatalf("CreateMatch: %v", err)
	}

	m, _ := s.MatchByID(mid)
	if m.Finished || m.ScoreA != nil {
		t.Error("new match should be unfinished with nil scores")
	}

	if err := s.SetResult(mid, 2, 1); err != nil {
		t.Fatalf("SetResult: %v", err)
	}
	m, _ = s.MatchByID(mid)
	if !m.Finished || m.ScoreA == nil || *m.ScoreA != 2 || *m.ScoreB != 1 {
		t.Errorf("result not stored: %+v", m)
	}

	if err := s.SetResult(999, 0, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for missing match, got %v", err)
	}
}

func TestSetPenaltiesRoundTrip(t *testing.T) {
	s := newTestStore(t)
	tid, _ := s.CreateTournament("T", true, testNow)
	mid, _ := s.CreateMatch(models.Match{
		TournamentID: tid, TeamA: "Germany", TeamB: "Paraguay",
		Phase: models.PhaseRound32, StartsAt: testNow,
	})
	if err := s.SetResult(mid, 1, 1); err != nil {
		t.Fatalf("SetResult: %v", err)
	}
	if err := s.SetPenalties(mid, 4, 2); err != nil {
		t.Fatalf("SetPenalties: %v", err)
	}
	m, _ := s.MatchByID(mid)
	if m.PenA == nil || m.PenB == nil || *m.PenA != 4 || *m.PenB != 2 {
		t.Fatalf("penalties not stored: %+v", m)
	}
	// 90' result is untouched.
	if *m.ScoreA != 1 || *m.ScoreB != 1 {
		t.Errorf("penalties should not change the 90' score: %+v", m)
	}
	// Re-recording the result clears the shootout.
	if err := s.SetResult(mid, 2, 1); err != nil {
		t.Fatalf("SetResult: %v", err)
	}
	m, _ = s.MatchByID(mid)
	if m.PenA != nil || m.PenB != nil {
		t.Errorf("SetResult should clear penalties, got %v-%v", m.PenA, m.PenB)
	}
	if err := s.SetPenalties(999, 1, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing match: want ErrNotFound, got %v", err)
	}
}

// Opening an existing DB again must be a no-op for the additive column
// migrations (the ALTERs tolerate "duplicate column name").
func TestMigrateIsIdempotent(t *testing.T) {
	path := t.TempDir() + "/idem.db"
	c1, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	c1.Close()
	c2, err := Open(path)
	if err != nil {
		t.Fatalf("second open (re-migrate): %v", err)
	}
	c2.Close()
}
