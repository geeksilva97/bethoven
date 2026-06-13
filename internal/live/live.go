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
	"sync"
	"time"
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

// Score is the live snapshot for a single match, oriented to our TeamA/TeamB.
type Score struct {
	A, B   int
	State  State
	Minute int
	Clock  string // display clock, e.g. "67'"
}

// Event is one fixture as reported by a Provider, before it is resolved to a
// stored match. Scores are keyed by home/away because feeds don't share our
// TeamA/TeamB ordering; the Poller orients them.
type Event struct {
	Home, Away           string
	HomeScore, AwayScore int
	Date                 time.Time // kickoff, UTC
	State                State
	Minute               int
	Clock                string
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

// Set records (or replaces) the live score for a match. Called by the Poller.
func (c *Cache) Set(matchID int64, s Score) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byMatch[matchID] = s
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
