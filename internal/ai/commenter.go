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
	Config  func() CommentConfig // default tone + per-player tones + rivalry/house context
	Now     func() time.Time
	// Results returns the recently finished matches + the pool's picks + the live
	// story, for the derived-notes snapshot. Optional — nil ⇒ no snapshot.
	Results func() (ResultsDigestData, error)
	// DerivedNotes returns the persisted derived-notes tier: the combined note text
	// to feed the prompt, plus the results signature the most recent note was built
	// from (so the worker regenerates only when results actually change). Optional —
	// nil ⇒ no derived-notes tier at all.
	DerivedNotes func() (combined string, sig string)
	// AddDerivedNote appends a freshly generated snapshot note + the signature it
	// was built from. Optional — nil ⇒ snapshots are never persisted/appended.
	AddDerivedNote func(text, sig string) error
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
		w.mon.record(CommentAction{At: w.deps.Now(), Outcome: "error", Err: sanitizeText(err.Error())})
		return
	}
	if len(history) == 0 {
		// No finished matches yet — nothing to comment on.
		return
	}

	cfg := w.deps.Config()
	cfg.Self = w.self // BETanIA's own line is first person

	pctx, cancel := context.WithTimeout(ctx, commentPassTimeout)
	defer cancel()

	// Derived notes: BETanIA's own "house notes" snapshot tier. When new matches
	// have settled since the last snapshot, summarize them (result + the pool's
	// picks + the live commentary story) and append a note. The combined notes feed
	// the per-player prompt as context. Best-effort — a digest fault never blocks the
	// comments themselves.
	cfg.DerivedNotes = w.refreshDerivedNotes(pctx, cfg)

	narratives, err := w.cmt.DetectNarratives(pctx, history)
	if err != nil {
		w.logger.Printf("ai: detect narratives: %v", err)
		w.mon.record(CommentAction{At: w.deps.Now(), Outcome: "error", Err: sanitizeText(err.Error())})
		return
	}
	comments, err := w.cmt.WriteComments(pctx, history, narratives, cfg)
	if err != nil {
		w.logger.Printf("ai: write comments: %v", err)
		w.mon.record(CommentAction{At: w.deps.Now(), Outcome: "error", Err: sanitizeText(err.Error())})
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
		if cfg.toneFor(c.Player) == "mute" {
			continue // never cache a muted player's comment, even if one slipped through
		}
		c.At = now
		c.ExpiresAt = now.Add(w.ttl)
		stamped = append(stamped, c)
		if err := appendCommentLog(w.logPath, cfg.toneFor(c.Player), now, c); err != nil {
			w.logger.Printf("ai: log comment for %s: %v", c.Player, err)
		}
		w.mon.record(CommentAction{At: now, Player: c.Player, Text: c.Text, Outcome: "written"})
	}
	w.cache.Replace(stamped)
	w.logger.Printf("ai: wrote %d comments (default tone=%s, %d narratives)", len(stamped), normalizeTone(cfg.DefaultTone), len(narratives))
}

// refreshDerivedNotes returns the combined derived-notes text to feed the comment
// prompt, generating + appending a fresh snapshot first when new matches have
// settled since the last one. All steps are optional/best-effort: missing seams or
// a digest error just yield whatever notes already exist (possibly ""). The seams
// (Results/DerivedNotes/AddDerivedNote) are nil unless wired, so this is a no-op
// for a worker built without them.
func (w *CommentWorker) refreshDerivedNotes(ctx context.Context, cfg CommentConfig) string {
	if w.deps.DerivedNotes == nil {
		return ""
	}
	combined, lastSig := w.deps.DerivedNotes()
	if w.deps.Results == nil || w.deps.AddDerivedNote == nil || w.cmt == nil {
		return combined
	}
	data, err := w.deps.Results()
	if err != nil {
		w.logger.Printf("ai: derived-notes results: %v", err)
		return combined
	}
	sig := digestSignature(data)
	if len(data.Matches) == 0 || sig == lastSig {
		return combined // nothing new to summarize
	}
	text, err := w.cmt.DigestResults(ctx, data, cfg)
	if err != nil {
		w.logger.Printf("ai: derived-notes digest: %v", err)
		return combined
	}
	text = sanitizeText(text)
	if text == "" {
		return combined
	}
	if err := w.deps.AddDerivedNote(text, sig); err != nil {
		w.logger.Printf("ai: save derived note: %v", err)
	}
	// Re-read so the prompt sees the freshly appended note alongside the prior ones.
	combined, _ = w.deps.DerivedNotes()
	return combined
}
