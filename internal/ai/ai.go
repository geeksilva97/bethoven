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
	"strings"
	"time"
	"unicode"

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

// sanitizeText strips control characters and ANSI escapes from untrusted model
// output (rationale, confidence) before it is logged or rendered. The model's
// free text is influenced by web-search results, so it's the same ANSI-injection
// boundary as display names — it lands in ai_bets.log (which an admin tails into
// a terminal) and in the admin BETanIA panel. Unlike service.cleanName we strip
// rather than reject (the text isn't user-correctable), mirroring live.cleanClock;
// whitespace runs collapse to a single space so a paragraph renders on one line.
func sanitizeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		// Consume whole escape sequences, not just the introducer: ESC (0x1b),
		// optionally '[', then parameter/intermediate bytes up to a final byte
		// (0x40-0x7e); also the 8-bit CSI introducer (0x9b). This removes
		// "\x1b[31m" entirely rather than leaving inert "[31m" residue.
		if r == 0x1b || r == 0x9b {
			if r == 0x1b && i+1 < len(rs) && rs[i+1] == '[' {
				i++
			}
			for i+1 < len(rs) {
				n := rs[i+1]
				i++
				if n >= 0x40 && n <= 0x7e {
					break
				}
			}
			continue
		}
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v':
			// collapse any run of ASCII whitespace to a single space
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
		case r < 0x20 || (r >= 0x7f && r <= 0x9f) || !unicode.IsPrint(r):
			// drop remaining C0/C1 control codes and non-printable runes
		default:
			b.WriteRune(r)
			prevSpace = false
		}
	}
	return strings.TrimSpace(b.String())
}

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
