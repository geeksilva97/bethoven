package ai

import (
	"context"
	"log"
	"time"
)

// commentPassTimeout bounds one full comment pass (two model calls over the whole
// table). Generous — the pass runs off the betting hot path.
const commentPassTimeout = 4 * time.Minute

// CommentDeps are the service seams the comment worker needs. Passing functions
// (not the service) keeps this package free of an import cycle, mirroring Deps.
type CommentDeps struct {
	History func() ([]RoundStanding, error)
	Tone    func() string
	Now     func() time.Time
}

// CommentWorker is BETanIA's commentary worker: on a timer it reconstructs the
// standings history, detects ranking narratives, writes one comment per player,
// and swaps them into the in-memory cache the leaderboard reads. Mirrors Bettor.
type CommentWorker struct {
	deps     CommentDeps
	cmt      Commenter
	cache    *CommentCache
	mon      *CommentMonitor
	self     string // BETanIA's own display name, so her line is written first-person
	interval time.Duration
	ttl      time.Duration // a comment is "fresh" until the next scheduled pass
	logPath  string
	logger   *log.Logger
	trigger  chan struct{} // manual "run now" requests (buffered, coalesced to 1)
}

// NewCommentWorker wires a comment worker. interval is the gap between passes and
// also the comment TTL (a comment stays current until the next pass would replace
// it). self is BETanIA's display name (her own comment is first-person).
func NewCommentWorker(deps CommentDeps, cmt Commenter, cache *CommentCache, mon *CommentMonitor, self string, interval time.Duration, logPath string) *CommentWorker {
	return &CommentWorker{
		deps:     deps,
		cmt:      cmt,
		cache:    cache,
		mon:      mon,
		self:     self,
		interval: interval,
		ttl:      interval,
		logPath:  logPath,
		logger:   log.Default(),
		trigger:  make(chan struct{}, 1),
	}
}

// Trigger requests an immediate pass (the admin "regenerate all" key). Non-blocking:
// if a pass is already queued it returns false and coalesces. Safe across goroutines.
func (w *CommentWorker) Trigger() bool {
	select {
	case w.trigger <- struct{}{}:
		return true
	default:
		return false
	}
}

// Run generates comments until ctx is cancelled. It fires once immediately, then
// on each tick or whenever a manual Trigger lands.
func (w *CommentWorker) Run(ctx context.Context) {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	w.pass(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.pass(ctx)
		case <-w.trigger:
			w.pass(ctx)
		}
	}
}

// pass runs a single detect-then-write pass and replaces the whole cache. It never
// panics out: any fault is logged and the worker carries on next tick.
func (w *CommentWorker) pass(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Printf("ai: recovered from panic in comment pass: %v", r)
		}
	}()

	w.mon.markRun(w.deps.Now())

	history, err := w.deps.History()
	if err != nil {
		w.logger.Printf("ai: comment history: %v", err)
		w.mon.record(CommentAction{At: w.deps.Now(), Outcome: "error", Err: err.Error()})
		return
	}
	if len(history) == 0 {
		// No finished matches yet — nothing to comment on.
		return
	}

	tone := normalizeTone(w.deps.Tone())

	pctx, cancel := context.WithTimeout(ctx, commentPassTimeout)
	defer cancel()

	narratives, err := w.cmt.DetectNarratives(pctx, history)
	if err != nil {
		w.logger.Printf("ai: detect narratives: %v", err)
		w.mon.record(CommentAction{At: w.deps.Now(), Outcome: "error", Err: err.Error()})
		return
	}
	comments, err := w.cmt.WriteComments(pctx, history, narratives, tone, w.self)
	if err != nil {
		w.logger.Printf("ai: write comments: %v", err)
		w.mon.record(CommentAction{At: w.deps.Now(), Outcome: "error", Err: err.Error()})
		return
	}

	now := w.deps.Now()
	stamped := make([]Comment, 0, len(comments))
	for _, c := range comments {
		// Sanitize here, at the cache/log boundary, so the leaderboard and the log
		// both get clean text regardless of the Commenter implementation — the same
		// ANSI-injection boundary as display names.
		c.Text = sanitizeText(c.Text)
		c.Player = sanitizeText(c.Player)
		if c.Text == "" {
			continue
		}
		c.At = now
		c.ExpiresAt = now.Add(w.ttl)
		stamped = append(stamped, c)
		if err := appendCommentLog(w.logPath, tone, now, c); err != nil {
			w.logger.Printf("ai: log comment for %s: %v", c.Player, err)
		}
		w.mon.record(CommentAction{At: now, Player: c.Player, Text: c.Text, Outcome: "written"})
	}
	w.cache.Replace(stamped)
	w.logger.Printf("ai: wrote %d comments (tone=%s, %d narratives)", len(stamped), tone, len(narratives))
}
