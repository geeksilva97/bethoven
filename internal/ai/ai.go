// Package ai adds BETanIA, an optional AI player, to BEThoven. It holds the
// pieces shared by two callers:
//
//   - the server's live Bettor (a background worker that bets UPCOMING matches
//     through the service, so the kickoff lock fully applies), and
//   - the `bethoven ai-seed` CLI, which backfills already-played matches once
//     (writing straight to the store — the one sanctioned lock bypass).
//
// Like internal/live and internal/analytics, this package never imports
// internal/service: the Bettor takes small function seams instead, so there is
// no import cycle (service imports ai for the AIMonitor port types).
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"bethoven/internal/models"
)

// Fingerprint is BETanIA's reserved identity. The non-"SHA256:" prefix means it
// can never collide with a real SSH key fingerprint, so it's a pure system account
// with no possible login. Both the seed CLI (which creates the user) and the
// server (which resolves it) key off this.
const Fingerprint = "bethoven:ai-betania"

// Prediction is one scoreline pick with the reasoning behind it. Scores are the
// regulation 90' result (so a 1-1 a.e.t. is a 1-1 draw, matching internal/scoring).
type Prediction struct {
	ScoreA, ScoreB int
	Rationale      string
	Confidence     string
}

// Predictor turns a match into a predicted scoreline. The concrete implementation
// is *AnthropicPredictor; tests use a fake. Keeping it an interface lets the Bettor
// and seeder stay testable without the network.
type Predictor interface {
	Predict(ctx context.Context, m models.Match) (Prediction, error)
}

// matchLabel is the human-readable "A vs B" used in logs and the admin feed.
func matchLabel(m models.Match) string { return m.TeamA + " vs " + m.TeamB }

// scoreText formats a prediction as "2-1".
func scoreText(p Prediction) string { return fmt.Sprintf("%d-%d", p.ScoreA, p.ScoreB) }

// logEntry is one line of ai_bets.log — the durable, on-disk record of every pick
// BETanIA makes, across both the seed and the live worker.
type logEntry struct {
	At         string `json:"at"`
	Source     string `json:"source"` // "seed" | "live"
	Match      string `json:"match"`
	Score      string `json:"score"`
	Confidence string `json:"confidence,omitempty"`
	Rationale  string `json:"rationale,omitempty"`
}

// appendLog appends one JSON line to path. A logging failure is returned to the
// caller, which treats it as non-fatal (the bet itself already succeeded).
func appendLog(path, source string, at time.Time, m models.Match, p Prediction) error {
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(logEntry{
		At:         at.UTC().Format(time.RFC3339),
		Source:     source,
		Match:      matchLabel(m),
		Score:      fmt.Sprintf("%d-%d", p.ScoreA, p.ScoreB),
		Confidence: p.Confidence,
		Rationale:  p.Rationale,
	})
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}
