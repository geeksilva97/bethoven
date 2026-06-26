package ai

import (
	"context"
	"encoding/json"
	"fmt"
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

// PlayerNote is an admin house note bound to ONE player (by display name), fed to the
// comment writer with explicit attribution so it can never be applied to a different
// player. The free-text Notes tier remains for genuinely pool-wide notes.
type PlayerNote struct {
	Player string
	Text   string
}

// CommentConfig carries everything the comment writer needs beyond the standings:
// the default tone, per-player tone overrides (by display name), BETanIA's own
// name (her line is first person), and admin context (rivalries + house notes).
type CommentConfig struct {
	DefaultTone string            // "playful" | "savage"
	Self        string            // BETanIA's display name; her line is first person
	ToneByName  map[string]string // display name -> "playful" | "savage" | "mute"; absent ⇒ default
	Rivalries   []Rivalry
	// PlayerNotes are house notes each bound to a single player (by display name).
	// Rendered with explicit "About <player>:" attribution so the model cannot reassign
	// one player's note to another — the structural fix for the cross-player note leak.
	PlayerNotes []PlayerNote
	// Notes are pool-wide house notes about nobody in particular (no player binding).
	Notes []string
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
	// Mood is BETanIA's current self-evolving mood (one of MoodValues; "" ⇒ neutral).
	// It's grounding fed into every line she writes so her voice drifts with how the
	// tournament is going for her. The director updates it; the service persists it.
	Mood string
	// PriorComments is the previous pass's line per player (keyed by display name),
	// fed back into the stage-2 prompt so a regeneration writes something FRESH
	// instead of recycling the same fact/phrasing (the per-player echo of the live
	// worker's anti-repeat history). Optional — empty ⇒ no prior-lines block (first
	// fill, or tests). The worker populates it from the comment cache.
	PriorComments map[string]string
	// LiveFocus is a transient, per-pass directive for the LIVE director only: the
	// single pool dynamic this line must cover (title race, a backmarker, a rivalry,
	// a climber/faller, who nailed/whiffed). The worker rotates it every line so the
	// commentary can't fixate on one hot player. Empty ⇒ no forced angle (the model
	// picks freely). Set by LiveCommentWorker.pass; never persisted.
	LiveFocus string
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
	// CompactNotes fuses the whole per-game derived-notes diary into ONE
	// consolidated narrative, weighting recent games most (the admin "compact"
	// action). Returns "" when there's nothing to compact.
	CompactNotes(ctx context.Context, notes []string, cfg CommentConfig) (string, error)
	// CompactHouseNotes fuses the admin's free-text house notes into ONE tighter
	// note, merging duplication while preserving every distinct fact (the admin
	// "compact house notes" action — a lossless merge, not a narrative). Returns ""
	// when there's nothing to compact.
	CompactHouseNotes(ctx context.Context, notes []string, cfg CommentConfig) (string, error)
	// UpdateRivalries inspects the standings (positions/movement/points) plus the
	// derived-note story and BETanIA's CURRENT auto-rivalries, and returns the full
	// desired set of auto-rivalries — declarative, so add/update/delete all fall out
	// of replacing the set. Grounded only in the data; nil/empty ⇒ no rivalries worth
	// tracking right now.
	UpdateRivalries(ctx context.Context, history []RoundStanding, derivedNotes string, existing []Rivalry, cfg CommentConfig) ([]Rivalry, error)
	// GeneratePlayerCard writes one player's end-of-tournament "hero's journey" card
	// from their trajectory + stats + notable picks (the admin "generate cards" /
	// per-card regen action). One grounded call, no web search. Returns "" when the
	// model produced nothing.
	GeneratePlayerCard(ctx context.Context, data CardDigestData, cfg CommentConfig) (string, error)
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

// MoodValues is the closed set of moods BETanIA's director may pick, mirroring
// narrativeTypes — a bounded vocabulary keeps her persona steerable and the value
// safe to render/store. "neutral" is the baseline; the rest react to how the
// tournament is treating her. The service validates stored moods against this set
// and the director's submit tool constrains its `mood` field to it.
var MoodValues = []string{"neutral", "cocky", "salty", "gracious", "nervous", "hyped"}

// NormalizeMood lower-cases and validates a mood against MoodValues, returning ""
// for anything outside the set (caller keeps the previous value). Exported so the
// service can validate the persisted setting against the same source of truth.
func NormalizeMood(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	for _, v := range MoodValues {
		if m == v {
			return v
		}
	}
	return ""
}

// MoodLine renders the one-line mood directive woven into both the live and the
// per-player prompts; "" when no (valid) mood is set, so a blank pool reads exactly
// as before. Shared so the two prompts phrase the mood identically.
func MoodLine(mood string) string {
	m := NormalizeMood(mood)
	if m == "" || m == "neutral" {
		return ""
	}
	return fmt.Sprintf("YOUR CURRENT MOOD is %q — let it colour your voice (word choice, energy), without ever inventing facts.\n", m)
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

// appendPlayerCardLog appends one JSON line per generated player card to path,
// tagged source:"player_card" so it's distinguishable from the roasts sharing the
// same log file. A logging failure is non-fatal (the card is already persisted).
func appendPlayerCardLog(path string, at time.Time, player, text string) error {
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
		Source: "player_card",
		Player: player,
		Text:   text,
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
