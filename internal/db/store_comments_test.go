package db

import (
	"testing"
	"time"

	"bethoven/internal/models"
)

func TestLeaderboardCommentUpsertAndLoad(t *testing.T) {
	s := newTestStore(t)
	u, _ := s.CreateUser("SHA256:c1", "Ana", models.RolePlayer, testNow)

	c := models.LeaderboardComment{UserID: u.ID, Player: "Ana", Text: "you're flying", CreatedAt: testNow}
	if err := s.UpsertLeaderboardComment(c); err != nil {
		t.Fatalf("UpsertLeaderboardComment: %v", err)
	}

	got, err := s.LeaderboardComments()
	if err != nil {
		t.Fatalf("LeaderboardComments: %v", err)
	}
	if len(got) != 1 || got[0].UserID != u.ID || got[0].Text != "you're flying" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	// Zero expires_at round-trips to the zero time (never-expires per-player comment).
	if !got[0].ExpiresAt.IsZero() {
		t.Errorf("ExpiresAt should be zero, got %v", got[0].ExpiresAt)
	}
	if !got[0].CreatedAt.Equal(testNow) {
		t.Errorf("CreatedAt round-trip: got %v want %v", got[0].CreatedAt, testNow)
	}

	// Upsert again replaces in place (one row per user).
	c.Text = "you slipped"
	if err := s.UpsertLeaderboardComment(c); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, _ = s.LeaderboardComments()
	if len(got) != 1 || got[0].Text != "you slipped" {
		t.Fatalf("upsert should replace in place: %+v", got)
	}
}

func TestReplaceLeaderboardCommentsDropsRemoved(t *testing.T) {
	s := newTestStore(t)
	ana, _ := s.CreateUser("SHA256:a", "Ana", models.RolePlayer, testNow)
	bob, _ := s.CreateUser("SHA256:b", "Bob", models.RolePlayer, testNow)

	first := []models.LeaderboardComment{
		{UserID: ana.ID, Player: "Ana", Text: "a1", CreatedAt: testNow},
		{UserID: bob.ID, Player: "Bob", Text: "b1", CreatedAt: testNow},
	}
	if err := s.ReplaceLeaderboardComments(first); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if got, _ := s.LeaderboardComments(); len(got) != 2 {
		t.Fatalf("want 2 rows, got %d", len(got))
	}

	// A new pass with only Ana must drop Bob's row entirely (no stale lines).
	if err := s.ReplaceLeaderboardComments([]models.LeaderboardComment{
		{UserID: ana.ID, Player: "Ana", Text: "a2", CreatedAt: testNow.Add(time.Hour)},
	}); err != nil {
		t.Fatalf("Replace 2: %v", err)
	}
	got, _ := s.LeaderboardComments()
	if len(got) != 1 || got[0].UserID != ana.ID || got[0].Text != "a2" {
		t.Fatalf("replace should leave only Ana@a2: %+v", got)
	}
}
