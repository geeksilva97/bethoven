package db

import (
	"testing"

	"bethoven/internal/models"
)

func TestPlayerCardUpsertAndLoad(t *testing.T) {
	s := newTestStore(t)
	u, _ := s.CreateUser("SHA256:c1", "Ana", models.RolePlayer, testNow)

	if err := s.UpsertPlayerCard(u.ID, "you started cold and ended hot", testNow); err != nil {
		t.Fatalf("UpsertPlayerCard: %v", err)
	}
	got, err := s.AllPlayerCards()
	if err != nil {
		t.Fatalf("AllPlayerCards: %v", err)
	}
	card, ok := got[u.ID]
	if !ok || card.Text != "you started cold and ended hot" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if !card.At.Equal(testNow) {
		t.Errorf("generated_at round-trip: got %v want %v", card.At, testNow)
	}

	// Upsert again replaces in place (one row per user).
	if err := s.UpsertPlayerCard(u.ID, "rewritten", testNow); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, _ = s.AllPlayerCards()
	if len(got) != 1 || got[u.ID].Text != "rewritten" {
		t.Fatalf("upsert should replace in place: %+v", got)
	}
}
