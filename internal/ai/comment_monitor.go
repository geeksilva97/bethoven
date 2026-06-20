package ai

import (
	"sync"
	"time"
)

// CommentAction is one comment BETanIA wrote (or a failed pass), for the admin feed.
type CommentAction struct {
	At      time.Time
	Player  string
	Text    string
	Outcome string // "written" | "error"
	Err     string // populated when Outcome == "error"
}

// CommentStatus is the comment worker's current state, for the admin status block.
type CommentStatus struct {
	Model    string
	Interval time.Duration
	LastRun  time.Time
	NextRun  time.Time // LastRun + Interval; zero before the first run
	Written  int
	Errored  int
}

// CommentMonitor is a concurrency-safe record of the comment worker's status and
// recent comments. The worker writes it; the service's AICommentMonitor port reads
// it for the admin panel. Mirrors Monitor.
type CommentMonitor struct {
	mu       sync.RWMutex
	model    string
	interval time.Duration
	lastRun  time.Time
	written  int
	errored  int
	recent   []CommentAction // oldest first; capped at monitorRing
}

// NewCommentMonitor returns a monitor seeded with the static config shown in the panel.
func NewCommentMonitor(model string, interval time.Duration) *CommentMonitor {
	return &CommentMonitor{model: model, interval: interval}
}

// Status returns a snapshot (implements service.AICommentMonitor).
func (mo *CommentMonitor) Status() CommentStatus {
	mo.mu.RLock()
	defer mo.mu.RUnlock()
	var next time.Time
	if !mo.lastRun.IsZero() {
		next = mo.lastRun.Add(mo.interval)
	}
	return CommentStatus{
		Model:    mo.model,
		Interval: mo.interval,
		LastRun:  mo.lastRun,
		NextRun:  next,
		Written:  mo.written,
		Errored:  mo.errored,
	}
}

// Activity returns the most recent comments, newest first (implements
// service.AICommentMonitor). limit <= 0 returns all retained actions.
func (mo *CommentMonitor) Activity(limit int) []CommentAction {
	mo.mu.RLock()
	defer mo.mu.RUnlock()
	n := len(mo.recent)
	if limit > 0 && limit < n {
		n = limit
	}
	out := make([]CommentAction, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, mo.recent[len(mo.recent)-1-i])
	}
	return out
}

// Seed backfills the monitor from comments restored at boot (the persisted set
// loaded into the cache), so the admin panel shows them — count, last run, and the
// recent-comments feed — instead of looking empty until the next pass. Without this
// a skip-regen restart would leave the admin view blank even though the comments are
// live on the leaderboard. Each entry is recorded as "written"; lastRun is advanced
// to the newest entry's time. Call once at startup, before the worker runs.
func (mo *CommentMonitor) Seed(comments []CommentAction, lastRun time.Time) {
	mo.mu.Lock()
	defer mo.mu.Unlock()
	for _, a := range comments {
		mo.recent = append(mo.recent, a)
		if a.Outcome == "written" {
			mo.written++
		}
	}
	if len(mo.recent) > monitorRing {
		mo.recent = mo.recent[len(mo.recent)-monitorRing:]
	}
	if lastRun.After(mo.lastRun) {
		mo.lastRun = lastRun
	}
}

// markRun stamps the start of a pass so NextRun can be derived.
func (mo *CommentMonitor) markRun(now time.Time) {
	mo.mu.Lock()
	defer mo.mu.Unlock()
	mo.lastRun = now
}

// record appends an action and bumps the matching counter.
func (mo *CommentMonitor) record(a CommentAction) {
	mo.mu.Lock()
	defer mo.mu.Unlock()
	switch a.Outcome {
	case "written":
		mo.written++
	case "error":
		mo.errored++
	}
	mo.recent = append(mo.recent, a)
	if len(mo.recent) > monitorRing {
		mo.recent = mo.recent[len(mo.recent)-monitorRing:]
	}
}
