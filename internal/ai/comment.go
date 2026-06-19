package ai

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// Comment is BETanIA's one-line take on a single player's leaderboard situation.
// Text is sanitized model output (sanitizeText) — it renders into players'
// terminals, the same ANSI-injection boundary as display names.
type Comment struct {
	UserID    int64
	Player    string // display name the comment is about
	Text      string
	At        time.Time
	ExpiresAt time.Time
}

// PlayerStanding is one player's rank at one point in the tournament. Movement is
// the change in position since the previous round (+ climbed, − fell, 0 at the
// first round or no change); PointsGained is the points added that round. These
// are computed by the service from finished matches — never stored — and handed to
// the narrative detector so "fell two places" is a grounded fact, not a guess.
type PlayerStanding struct {
	UserID       int64
	Name         string
	Position     int
	Total        int
	Movement     int
	PointsGained int
}

// RoundStanding is the full table after one round (one matchday). Rounds arrive
// oldest→newest; the last entry is the current settled table.
type RoundStanding struct {
	Label string // e.g. "2026-06-15"
	Ranks []PlayerStanding
}

// Narrative is one detected story in the standings (a rivalry, a free-fall, a
// leadership battle). It is the grounded, fact-only intermediate the comment
// writer turns into prose. Mirrors the "Ranking Narrative Detection" brief.
type Narrative struct {
	Type         string   `json:"type"`
	Summary      string   `json:"summary"`
	Participants []string `json:"participants"`
	Confidence   float64  `json:"confidence"`
}

// Commenter turns standings history into per-player comments in two grounded
// stages: detect narratives (facts only), then write the lines in a given tone.
// The concrete implementation is *AnthropicCommenter; tests use a fake. Keeping
// it an interface lets the worker stay testable without the network.
// Rivalry is an admin-entered note about two players, fed to the comment writer
// so it can weave a real-world rivalry into the lines. A/B are display names.
type Rivalry struct {
	A, B string
	Note string
}

// CommentConfig carries everything the comment writer needs beyond the standings:
// the default tone, per-player tone overrides (by display name), BETanIA's own
// name (her line is first person), and admin context (rivalries + house notes).
type CommentConfig struct {
	DefaultTone string            // "playful" | "savage"
	Self        string            // BETanIA's display name; her line is first person
	ToneByName  map[string]string // display name -> "playful" | "savage" | "mute"; absent ⇒ default
	Rivalries   []Rivalry
	Notes       []string
}

// toneFor returns the effective tone for a player: their override, or the default.
func (c CommentConfig) toneFor(name string) string {
	if t, ok := c.ToneByName[name]; ok && t != "" {
		return t
	}
	return normalizeTone(c.DefaultTone)
}

// Commenter turns standings + config into per-player comments, in two grounded
// stages. The concrete implementation is *AnthropicCommenter; tests use a fake.
type Commenter interface {
	DetectNarratives(ctx context.Context, history []RoundStanding) ([]Narrative, error)
	WriteComments(ctx context.Context, history []RoundStanding, narratives []Narrative, cfg CommentConfig) ([]Comment, error)
}

// narrativeTypes is the closed vocabulary the detector may use (from the brief).
// Constraining the type keeps narratives machine-checkable and on-theme.
var narrativeTypes = []string{
	"dominant_leader", "leader_under_pressure", "leadership_rivalry",
	"hunting_target", "being_hunted", "trapped_between_rivals",
	"on_the_rise", "free_fall", "stuck_in_place",
	"crowded_pack", "midfield_chaos", "no_mans_land",
	"bottom_escape_attempt", "deep_in_the_basement", "personal_rivalry",
	"comeback_story", "fallen_king", "eternal_runner_up",
	"boring_consistency", "one_hit_wonder",
}

// normalizeTone collapses any tone to the two we support; unknown ⇒ playful.
func normalizeTone(t string) string {
	if strings.EqualFold(strings.TrimSpace(t), "savage") {
		return "savage"
	}
	return "playful"
}

// commentLogEntry is one line of the comment log — the durable record of every
// roast BETanIA writes, the comment-side sibling of logEntry.
type commentLogEntry struct {
	At     string `json:"at"`
	Source string `json:"source"` // always "comment"
	Player string `json:"player"`
	Tone   string `json:"tone,omitempty"`
	Text   string `json:"text"`
}

// appendCommentLog appends one JSON line per comment to path. A logging failure is
// returned and treated as non-fatal by the caller (the comment is already cached).
func appendCommentLog(path, tone string, at time.Time, c Comment) error {
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(commentLogEntry{
		At:     at.UTC().Format(time.RFC3339),
		Source: "comment",
		Player: c.Player,
		Tone:   tone,
		Text:   c.Text,
	})
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}
