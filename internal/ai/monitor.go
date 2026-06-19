package ai

import (
	"sync"
	"time"
)

// monitorRing caps how many recent actions the in-memory feed keeps.
const monitorRing = 50

// Action is one decision BETanIA's live worker made, for the admin activity feed.
type Action struct {
	At         time.Time
	Match      string // "A vs B"
	Score      string // "2-1" (empty on error before a prediction)
	Rationale  string
	Confidence string
	Outcome    string // "placed" | "locked" | "error"
	Err        string // populated when Outcome == "error"
}

// Status is the live worker's current state, for the admin status block.
type Status struct {
	Model    string
	Interval time.Duration
	LastRun  time.Time
	NextRun  time.Time // LastRun + Interval; zero before the first run
	Placed   int
	Skipped  int
	Locked   int
	Errored  int
}

// Monitor is a concurrency-safe record of the live worker's status and recent
// actions. The Bettor writes it; the service's AIMonitor port reads it for the
// admin panel. Mirrors live.Cache's mutex-guarded shape.
type Monitor struct {
	mu       sync.RWMutex
	model    string
	interval time.Duration
	lastRun  time.Time
	placed   int
	skipped  int
	locked   int
	errored  int
	recent   []Action // oldest first; capped at monitorRing
}

// NewMonitor returns a Monitor seeded with the static config shown in the panel.
func NewMonitor(model string, interval time.Duration) *Monitor {
	return &Monitor{model: model, interval: interval}
}

// Status returns a snapshot of the current status (implements service.AIMonitor).
func (mo *Monitor) Status() Status {
	mo.mu.RLock()
	defer mo.mu.RUnlock()
	var next time.Time
	if !mo.lastRun.IsZero() {
		next = mo.lastRun.Add(mo.interval)
	}
	return Status{
		Model:    mo.model,
		Interval: mo.interval,
		LastRun:  mo.lastRun,
		NextRun:  next,
		Placed:   mo.placed,
		Skipped:  mo.skipped,
		Locked:   mo.locked,
		Errored:  mo.errored,
	}
}

// Activity returns the most recent actions, newest first (implements
// service.AIMonitor). limit <= 0 returns all retained actions.
func (mo *Monitor) Activity(limit int) []Action {
	mo.mu.RLock()
	defer mo.mu.RUnlock()
	n := len(mo.recent)
	if limit > 0 && limit < n {
		n = limit
	}
	out := make([]Action, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, mo.recent[len(mo.recent)-1-i]) // newest first
	}
	return out
}

// markRun stamps the start of a betting pass so NextRun can be derived.
func (mo *Monitor) markRun(now time.Time) {
	mo.mu.Lock()
	defer mo.mu.Unlock()
	mo.lastRun = now
}

// skip bumps the skipped counter (already-bet or out-of-window matches); it does
// not create an Action, to keep the feed signal-rich.
func (mo *Monitor) skip() {
	mo.mu.Lock()
	defer mo.mu.Unlock()
	mo.skipped++
}

// record appends an action and bumps the matching counter.
func (mo *Monitor) record(a Action) {
	mo.mu.Lock()
	defer mo.mu.Unlock()
	switch a.Outcome {
	case "placed":
		mo.placed++
	case "locked":
		mo.locked++
	case "error":
		mo.errored++
	}
	mo.recent = append(mo.recent, a)
	if len(mo.recent) > monitorRing {
		mo.recent = mo.recent[len(mo.recent)-monitorRing:]
	}
}
