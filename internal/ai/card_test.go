package ai

import (
	"context"
	"strings"
	"testing"
	"time"
)

// cardPrompt must weave in every memory tier relevant to the player: their derived
// "story", a rivalry they're in, a house note about them, a pool-wide note, and their
// PER-PLAYER tone (not just the pool default).
func TestCardPromptIncludesMemoryTiers(t *testing.T) {
	data := CardDigestData{
		UserID: 1, Player: "Joao", FinalRank: 1, TotalPlayers: 4,
		Story: "On Jun 14 Joao nailed the Spain upset.",
	}
	cfg := CommentConfig{
		DefaultTone: "playful",
		ToneByName:  map[string]string{"Joao": "savage"},
		Rivalries: []Rivalry{
			{A: "Joao", B: "Ana", Note: "office derby"},
			{A: "Bob", B: "Carl", Note: "unrelated pair"},
		},
		PlayerNotes: []PlayerNote{
			{Player: "Joao", Text: "the reigning champion"},
			{Player: "Ana", Text: "someone else's note"},
		},
		Notes: []string{"played in the break room"},
	}
	p := cardPrompt(data, cfg)

	if !strings.Contains(p, "TONE: savage") {
		t.Error("per-player tone override (savage) not applied to the card")
	}
	if !strings.Contains(p, "office derby") {
		t.Error("the player's rivalry note is missing")
	}
	if !strings.Contains(p, "Rivalry with Ana") {
		t.Error("rivalry should name the OTHER player")
	}
	if strings.Contains(p, "unrelated pair") {
		t.Error("a rivalry not involving the player must be excluded")
	}
	if !strings.Contains(p, "the reigning champion") {
		t.Error("the house note about the player is missing")
	}
	if strings.Contains(p, "someone else's note") {
		t.Error("another player's note must not leak into this card")
	}
	if !strings.Contains(p, "played in the break room") {
		t.Error("the pool-wide note is missing")
	}
	if !strings.Contains(p, "On Jun 14 Joao nailed the Spain upset.") {
		t.Error("the derived-notes story is missing")
	}
}

// GenerateCards writes one narrative per player, sanitized, and persists each via the
// SaveCard seam.
func TestGenerateCardsWritesAndSanitizes(t *testing.T) {
	now := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	fc := &fakeCommenter{card: "you climbed \x1b[31mhard\x1b[0m and learned to trust the draws"}
	saved := map[int64]string{}
	w := NewCommentWorker(CommentDeps{
		Config: func() CommentConfig { return CommentConfig{DefaultTone: "playful"} },
		Now:    func() time.Time { return now },
		CardDigests: func() ([]CardDigestData, error) {
			return []CardDigestData{
				{UserID: 1, Player: "Joao", FinalRank: 1},
				{UserID: 2, Player: "Ana", FinalRank: 2},
			}, nil
		},
		SaveCard: func(userID int64, narrative string) error { saved[userID] = narrative; return nil },
	}, fc, NewCommentCache(), NewCommentMonitor("t", time.Hour), "", "")

	if err := w.GenerateCards(context.Background()); err != nil {
		t.Fatalf("GenerateCards: %v", err)
	}
	if fc.cardCalls != 2 {
		t.Fatalf("GeneratePlayerCard calls = %d, want 2", fc.cardCalls)
	}
	if len(saved) != 2 {
		t.Fatalf("saved %d cards, want 2", len(saved))
	}
	if saved[1] != "you climbed hard and learned to trust the draws" {
		t.Errorf("card not sanitized: %q", saved[1])
	}
}

// GenerateCard writes and persists a single player's card, returning the text.
func TestGenerateCardSingle(t *testing.T) {
	now := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	fc := &fakeCommenter{card: "your one and only card"}
	var savedID int64
	w := NewCommentWorker(CommentDeps{
		Config: func() CommentConfig { return CommentConfig{DefaultTone: "savage"} },
		Now:    func() time.Time { return now },
		CardDigest: func(userID int64) (CardDigestData, error) {
			return CardDigestData{UserID: userID, Player: "Joao"}, nil
		},
		SaveCard: func(userID int64, narrative string) error { savedID = userID; return nil },
	}, fc, NewCommentCache(), NewCommentMonitor("t", time.Hour), "", "")

	txt, err := w.GenerateCard(context.Background(), 7)
	if err != nil {
		t.Fatalf("GenerateCard: %v", err)
	}
	if txt != "your one and only card" || savedID != 7 {
		t.Errorf("GenerateCard = %q for id %d; want the card for 7", txt, savedID)
	}
}

// With the card seams unwired, GenerateCards is a no-op and GenerateCard errors.
func TestGenerateCardsSeamsOff(t *testing.T) {
	w := NewCommentWorker(CommentDeps{
		Config: func() CommentConfig { return CommentConfig{} },
		Now:    func() time.Time { return time.Time{} },
	}, &fakeCommenter{}, NewCommentCache(), NewCommentMonitor("t", time.Hour), "", "")

	if err := w.GenerateCards(context.Background()); err != nil {
		t.Errorf("GenerateCards with no seams should be a no-op, got %v", err)
	}
	if _, err := w.GenerateCard(context.Background(), 1); err == nil {
		t.Error("GenerateCard with no seams should error")
	}
}
