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
	// DerivedNotes is BETanIA's own auto-generated "house notes" snapshot: a short
	// condensation of recently finished matches and how the pool's picks fared,
	// produced by DigestResults and refreshed when a match settles. It's a SEPARATE
	// tier from the admin's Notes — context the writer may weave in, never the
	// admin's curated text. Empty ⇒ no snapshot yet.
	DerivedNotes string
	// PromptOverride, when non-empty, REPLACES the built-in persona/tone/rules
	// instruction body of the stage-2 comment prompt. The harness still appends
	// the untrusted-data trailer + standings JSON + the submit_comments line, and
	// the submit_comments tool stays attached. Empty ⇒ the built-in prompt verbatim.
	PromptOverride string
	// Steering is optional one-off admin guidance for a SINGLE regeneration (the
	// "regenerate this one" action). When non-empty it's appended to the stage-2
	// prompt as extra direction for this pass only — it's never persisted and is
	// empty on the scheduled worker passes. It's admin-trusted steering but framed
	// in the prompt as guidance that never overrides the grounding rules.
	Steering string
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
	// DigestResults condenses recently finished matches + how the pool's picks
	// fared into a short "house notes" snapshot (the extra summary call). Returns
	// "" when there's nothing settled to summarize.
	DigestResults(ctx context.Context, data ResultsDigestData, cfg CommentConfig) (string, error)
}

// DigestPick is one player's pick on a finished match, for the results snapshot.
type DigestPick struct {
	Player string `json:"player"`
	Pred   string `json:"pred"`   // "2-1"
	Points int    `json:"points"` // points scored under the active mode
}

// FinishedMatchDigest is a settled match plus how the pool bet it — the raw
// material DigestResults condenses into the derived-notes snapshot.
type FinishedMatchDigest struct {
	TeamA string       `json:"team_a"`
	TeamB string       `json:"team_b"`
	Score string       `json:"score"` // regulation 90' result, "2-1"
	Stage string       `json:"stage,omitempty"`
	Picks []DigestPick `json:"picks"`
}

// ResultsDigestData is the input to DigestResults for ONE finished match: its
// result, every player's pick on it, and the live-commentary lines that played
// while it was on — so the note tells the STORY of that game, not just the score.
// (Matches is a one-element slice for the per-game note; the field is plural for
// JSON stability.) MatchID identifies the game so the service notes it exactly once.
type ResultsDigestData struct {
	MatchID int64                 `json:"-"`
	Matches []FinishedMatchDigest `json:"matches"`
	// LiveStory is BETanIA's own play-by-play from during the match (recovered
	// from the comment log; oldest first). Optional grounding for the narrative.
	LiveStory []string `json:"live_story,omitempty"`
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

// appendLiveCommentLog appends one JSON line per live-commentary line to path,
// tagged source:"live_comment" so it's distinguishable from the per-player roasts
// sharing the same log file. A logging failure is non-fatal (the line is cached).
func appendLiveCommentLog(path string, at time.Time, text string) error {
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
		Source: "live_comment",
		Text:   text,
	})
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}
