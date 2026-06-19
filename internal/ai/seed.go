package ai

import (
	"context"
	"log"
	"time"

	"bethoven/internal/db"
	"bethoven/internal/models"
)

// SeedResult summarises a seeding pass for the CLI to print.
type SeedResult struct {
	Placed     int // matches newly seeded this run
	AlreadyHad int // past matches BETanIA had already bet (skipped)
}

// SeedPastGames backfills BETanIA's picks for already-played matches, using the
// provided (web-search-OFF) predictor. It writes straight to the store via
// UpsertBet — the one sanctioned kickoff-lock bypass — because those matches are
// kicked off and the service's PlaceBet would (correctly) reject them.
//
// It is idempotent: matches BETanIA already has a bet on are skipped, so a re-run
// places nothing new and never re-spends the API. `now` decides which matches
// count as past (now >= StartsAt).
func SeedPastGames(ctx context.Context, store *db.Store, tournamentID int64, pred Predictor, userID int64, now time.Time, logPath string, logger *log.Logger) (SeedResult, error) {
	if logger == nil {
		logger = log.Default()
	}
	matches, err := store.ListMatches(tournamentID)
	if err != nil {
		return SeedResult{}, err
	}
	existing, err := store.BetsForUser(userID, tournamentID)
	if err != nil {
		return SeedResult{}, err
	}
	bet := make(map[int64]bool, len(existing))
	for _, b := range existing {
		bet[b.MatchID] = true
	}

	var res SeedResult
	for _, m := range matches {
		if now.Before(m.StartsAt) {
			continue // upcoming — the live worker handles these (and the lock allows them)
		}
		if bet[m.ID] {
			res.AlreadyHad++
			continue
		}
		if ctx.Err() != nil {
			return res, ctx.Err()
		}

		pctx, cancel := context.WithTimeout(ctx, perMatchTimeout)
		pred, err := pred.Predict(pctx, m)
		cancel()
		if err != nil {
			logger.Printf("ai-seed: predict %s: %v", matchLabel(m), err)
			continue
		}
		// Direct store write — bypasses the kickoff lock by design (these games are
		// over). Sanctioned, one-time, admin-run; same mechanism as `place-bet`.
		if err := store.UpsertBet(models.Bet{
			UserID:  userID,
			MatchID: m.ID,
			PredA:   pred.ScoreA,
			PredB:   pred.ScoreB,
		}, now); err != nil {
			logger.Printf("ai-seed: write %s: %v", matchLabel(m), err)
			continue
		}
		if err := appendLog(logPath, "seed", now, m, pred); err != nil {
			logger.Printf("ai-seed: log %s: %v", matchLabel(m), err)
		}
		res.Placed++
		logger.Printf("ai-seed: %s %s (%s)", matchLabel(m), scoreText(pred), pred.Confidence)
	}
	return res, nil
}
