package main

import (
	"context"
	"errors"
	"log"
	"os"
	"time"

	"bethoven/internal/ai"
	"bethoven/internal/config"
	"bethoven/internal/db"
	"bethoven/internal/models"
)

// runAISeed onboards BETanIA: it creates the player (if absent) and backfills
// already-played matches with predictions made WITH WEB SEARCH OFF — pure
// pre-tournament knowledge, so it isn't hindsight (the 2026 results post-date the
// model's training cutoff and there's no internet to look them up). It writes
// straight to the store, the one sanctioned kickoff-lock bypass (the sibling of
// `bethoven place-bet`). Idempotent: re-running places nothing new.
//
// Usage: bethoven ai-seed
func runAISeed(args []string) {
	cfg := config.Load()
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		log.Fatalf("ai-seed: ANTHROPIC_API_KEY must be set")
	}

	conn, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open db %s: %v", cfg.DBPath, err)
	}
	defer conn.Close()
	store := db.NewStore(conn)

	t, err := store.ActiveTournament()
	if err != nil {
		log.Fatalf("active tournament: %v", err)
	}

	userID, created, err := ensureAIPlayer(store, cfg.AIName)
	if err != nil {
		log.Fatalf("ai-seed: ensure player: %v", err)
	}
	if created {
		log.Printf("ai-seed: created player %q (%s)", cfg.AIName, ai.Fingerprint)
	} else {
		log.Printf("ai-seed: player %q already exists", cfg.AIName)
	}

	// Web search OFF: the seed must be unbiased pre-tournament knowledge.
	pred := ai.NewAnthropicPredictor(cfg.AIModel, false)
	res, err := ai.SeedPastGames(context.Background(), store, t.ID, pred, userID, time.Now().UTC(), cfg.AILogPath, log.Default())
	if err != nil {
		log.Fatalf("ai-seed: %v", err)
	}
	log.Printf("ai-seed: done — seeded %d past match(es); %d already had picks", res.Placed, res.AlreadyHad)
}

// ensureAIPlayer returns BETanIA's user id, creating the player on first run.
// created is true only when this call inserted the row. User creation lives here
// (the onboarding script), never in server startup.
func ensureAIPlayer(store *db.Store, name string) (id int64, created bool, err error) {
	if u, err := store.UserByFingerprint(ai.Fingerprint); err == nil {
		return u.ID, false, nil
	} else if !errors.Is(err, db.ErrNotFound) {
		return 0, false, err
	}
	u, err := store.CreateUser(ai.Fingerprint, name, models.RolePlayer, time.Now().UTC())
	if err != nil {
		return 0, false, err
	}
	return u.ID, true, nil
}
