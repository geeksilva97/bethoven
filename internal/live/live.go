// Package live adds optional, in-memory live scores to BEThoven. A Provider
// fetches the current state of matches from an external feed; a Poller resolves
// each reported fixture to one of our stored matches and writes the result into
// a Cache. The service reads the Cache to fold provisional points into the
// leaderboard and to show running scores — none of it is persisted, so a
// restart simply re-fetches within one poll cycle.
//
// The whole package sits behind the service.LiveStore port (the Cache), so the
// data source is swappable and the service stays testable with a fake snapshot.
package live

import (
	"context"
	"strings"
	"sync"
	"time"

	"bethoven/internal/models"
)

// State is where a match is in its lifecycle, per the feed.
type State int

const (
	StatePre  State = iota // not started — never revealed (preserves blind betting)
	StateIn                // in play
	StatePost              // finished
)

// ParseState maps an ESPN status.type.state string ("pre"/"in"/"post") to State.
func ParseState(s string) State {
	switch s {
	case "in":
		return StateIn
	case "post":
		return StatePost
	default:
		return StatePre
	}
}

// Phase is a finer, in-play breakdown than State — the match is "in" but paused or
// past regulation. Empty means ordinary live play. These are OUR controlled labels
// (a closed vocabulary), never raw feed text, so they're safe to render and to feed
// the model. ParsePhase derives them from ESPN's status.type.name.
const (
	PhaseHalftime  = "halftime"   // the interval — comment as a break, not live play
	PhaseExtraTime = "extra_time" // knockout extra time (goals here DO count — we score 120')
	PhasePenalties = "penalties"  // shootout — the 120' score is final; pens never change points
)

// ParsePhase maps an ESPN status to one of our controlled Phase labels, or "" for
// ordinary in-play. It reads both status.type.name ("STATUS_HALFTIME", …) and the
// human status.type.shortDetail ("AET-pens", "HT", …), because the earliest signal a
// match is going to penalties is shortDetail "AET-pens" while name is still
// STATUS_END_OF_EXTRATIME (which contains "EXTRA"). Penalties is therefore checked
// BEFORE extra time, and across BOTH fields, so we detect the shootout the moment
// ESPN flags it — not only once the name flips to STATUS_SHOOTOUT. Substring matching
// (uppercased) keeps it robust to ESPN's naming variants.
func ParsePhase(name, shortDetail string) string {
	s := strings.ToUpper(name + " " + shortDetail)
	switch {
	case strings.Contains(s, "HALFTIME"):
		return PhaseHalftime
	case strings.Contains(s, "PEN"), strings.Contains(s, "SHOOTOUT"):
		return PhasePenalties
	case strings.Contains(s, "EXTRA"):
		return PhaseExtraTime
	default:
		return ""
	}
}

// Score is the live snapshot for a single match, oriented to our TeamA/TeamB.
type Score struct {
	A, B   int
	State  State
	Minute int                 // feed "period" (half number), not a clock minute; UI shows Clock
	Clock  string              // display clock, e.g. "67'"
	Phase  string              // controlled in-play phase label (PhaseHalftime, …); "" for ordinary play
	Odds   string              // sanitized pre-match odds, e.g. "USA -160 · O/U 2.5"; empty if absent
	Events []models.MatchEvent // sanitized key events (goals/cards), oldest→newest; the text references team names directly, so no TeamA/TeamB orientation
}

// Event is one fixture as reported by a Provider, before it is resolved to a
// stored match. Scores are keyed by home/away because feeds don't share our
// TeamA/TeamB ordering; the Poller orients them.
type Event struct {
	ID                   string // provider event id, used to fetch the summary (key events)
	Home, Away           string
	HomeScore, AwayScore int
	Date                 time.Time // kickoff, UTC
	State                State
	Minute               int
	Clock                string
	Phase                string              // controlled in-play phase label (PhaseHalftime, …); "" for ordinary play
	Odds                 string              // sanitized pre-match odds; references team names directly, so no orientation
	KeyEvents            []models.MatchEvent // sanitized goals/cards; populated only for in-play events
}

// Provider fetches the current live state for the given UTC days. Implementations
// must be safe to call repeatedly and should return a nil/empty slice (not an
// error) when there is simply nothing live.
type Provider interface {
	Fetch(ctx context.Context, days []time.Time) ([]Event, error)
}

// Cache is a concurrency-safe, in-memory map of live scores keyed by our match
// ID. It implements the service.LiveStore port via Snapshot.
type Cache struct {
	mu      sync.RWMutex
	byMatch map[int64]Score
}

// NewCache returns an empty Cache.
func NewCache() *Cache { return &Cache{byMatch: make(map[int64]Score)} }

// Replace atomically swaps the entire set of live scores. The poller rebuilds
// the map every poll so a match the feed stops reporting — a gap, or a missed
// finish while the server was down — drops out instead of lingering forever as
// a stale in-play score (which would keep adding phantom provisional points).
func (c *Cache) Replace(scores map[int64]Score) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byMatch = scores
}

// Snapshot returns a copy of the current live scores, safe for the caller to
// read without holding the lock.
func (c *Cache) Snapshot() map[int64]Score {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[int64]Score, len(c.byMatch))
	for k, v := range c.byMatch {
		out[k] = v
	}
	return out
}
