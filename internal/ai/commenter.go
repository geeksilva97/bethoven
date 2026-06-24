package ai

import (
	"context"
	"fmt"
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
	// PendingDigests returns one digest input per finished match that has no derived
	// note yet (one note per game). Optional — nil ⇒ no derived-notes tier.
	PendingDigests func() ([]ResultsDigestData, error)
	// DerivedNotes returns the combined per-game stories to feed the comment prompt.
	// Optional — nil ⇒ no derived-notes tier at all.
	DerivedNotes func() string
	// AddDerivedNote stores one finished match's story (keyed by its match id so it's
	// narrated once). Optional — nil ⇒ stories are never persisted.
	AddDerivedNote func(matchID int64, text string) error
	// AutoRivalries returns BETanIA's CURRENT self-managed rivalries (resolved to
	// names), so the detector keeps stable ones rather than churning. Optional.
	AutoRivalries func() []Rivalry
	// SetAutoRivalries persists BETanIA's desired auto-rivalry set. The service merges
	// in any pinned ones and dedups against admin rivalries. Optional — nil (or a nil
	// AutoRivalries) ⇒ the self-managed rivalry tier is disabled entirely.
	SetAutoRivalries func(rivals []Rivalry) error
	// SaveComments persists the full per-player comment set after a pass (write-through
	// to the DB so they survive a restart). SaveComment persists a single regenerated
	// comment. Both optional — nil ⇒ comments stay in-memory only (prior behaviour).
	SaveComments func(comments []Comment) error
	SaveComment  func(comment Comment) error
}

// CommentWorker is BETanIA's per-player commentary worker. It has NO timer of its
// own — BETanIA's director owns the cadence. It reconstructs the standings history,
// detects ranking narratives, writes one comment per player, and swaps them into the
// in-memory cache the leaderboard reads. It runs once at startup, then only when
// triggered: a match settling (onMatchSettled), the admin "regenerate all" key, or —
// per player — the director's RegenerateOne. Written comments never expire on a clock;
// they persist until the next pass replaces them.
type CommentWorker struct {
	deps    CommentDeps
	cmt     Commenter
	cache   *CommentCache
	mon     *CommentMonitor
	self    string // BETanIA's own display name, so her line is written first-person
	logPath string
	logger  *log.Logger
	trigger chan struct{} // manual "run now" requests (buffered, coalesced to 1)
}

// NewCommentWorker wires a comment worker. self is BETanIA's display name (her own
// comment is first-person). There is no interval — the worker fires once at startup
// and thereafter only on a trigger (a settle, the admin key) or a per-player regen.
func NewCommentWorker(deps CommentDeps, cmt Commenter, cache *CommentCache, mon *CommentMonitor, self, logPath string) *CommentWorker {
	return &CommentWorker{
		deps:    deps,
		cmt:     cmt,
		cache:   cache,
		mon:     mon,
		self:    self,
		logPath: logPath,
		logger:  log.Default(),
		trigger: make(chan struct{}, 1),
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

// Run generates comments until ctx is cancelled. It fires once immediately to fill
// the board, then ONLY when a Trigger lands — there is no periodic pass. The cadence
// is owned by the director (per-player regens) and match settlements (full passes).
func (w *CommentWorker) Run(ctx context.Context) {
	// Only regenerate at boot if there's nothing to show. When persisted comments
	// were restored into the cache before Run (a normal restart/deploy), skip the
	// pass entirely — no match settled while we were down, so the comments aren't
	// stale, and regenerating would just re-spend tokens. Fresh state (empty cache)
	// still fills the board once.
	if w.cache == nil || w.cache.Empty() {
		w.pass(ctx)
	} else {
		w.logger.Printf("ai: persisted comments restored — skipping boot regeneration")
	}
	for {
		select {
		case <-ctx.Done():
			return
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
	cfg.PriorComments = w.priorComments()

	pctx, cancel := context.WithTimeout(ctx, commentPassTimeout)
	defer cancel()

	// Derived notes: BETanIA's own "house notes" snapshot tier. When new matches
	// have settled since the last snapshot, summarize them (result + the pool's
	// picks + the live commentary story) and append a note. The combined notes feed
	// the per-player prompt as context. Best-effort — a digest fault never blocks the
	// comments themselves.
	cfg.DerivedNotes = w.refreshDerivedNotes(pctx, cfg)

	// Self-managed rivalries: from the standings + the story so far, BETanIA decides
	// which rivalries to track (add/update/drop), persists them, and we re-read the
	// merged set so THIS pass's per-player lines already weave them in. Best-effort.
	cfg.Rivalries = w.refreshAutoRivalries(pctx, cfg, history)

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
		c.ExpiresAt = time.Time{} // never expires on a clock — replaced by the next pass
		stamped = append(stamped, c)
		if err := appendCommentLog(w.logPath, cfg.toneFor(c.Player), now, c); err != nil {
			w.logger.Printf("ai: log comment for %s: %v", c.Player, err)
		}
		w.mon.record(CommentAction{At: now, Player: c.Player, Text: c.Text, Outcome: "written"})
	}
	w.cache.Replace(stamped)
	// Write through to the DB so the set survives a restart (best-effort — a persist
	// fault must never blank the leaderboard the cache just got right).
	if w.deps.SaveComments != nil {
		if err := w.deps.SaveComments(stamped); err != nil {
			w.logger.Printf("ai: persist comments: %v", err)
		}
	}
	w.logger.Printf("ai: wrote %d comments (default tone=%s, %d narratives)", len(stamped), normalizeTone(cfg.DefaultTone), len(narratives))
}

// RegenerateOne rewrites a SINGLE player's comment on demand (the admin "regenerate
// this one" action) and upserts it into the cache, leaving every other player's
// line untouched. Synchronous — the caller (service, via a tea.Cmd) supplies a
// context with a timeout. Mirrors a normal pass (detect → write) but keeps only the
// targeted player's line. extra is optional one-off admin steering for this pass
// only (empty ⇒ a plain regeneration). Returns the new comment, or an error when
// there's no history, the player is muted, or the model didn't produce a line.
func (w *CommentWorker) RegenerateOne(ctx context.Context, userID int64, extra string) (Comment, error) {
	history, err := w.deps.History()
	if err != nil {
		return Comment{}, err
	}
	if len(history) == 0 {
		return Comment{}, fmt.Errorf("no finished matches yet")
	}

	cfg := w.deps.Config()
	cfg.Self = w.self
	cfg.Steering = extra
	cfg.PriorComments = w.priorComments()

	narratives, err := w.cmt.DetectNarratives(ctx, history)
	if err != nil {
		return Comment{}, err
	}
	comments, err := w.cmt.WriteComments(ctx, history, narratives, cfg)
	if err != nil {
		return Comment{}, err
	}

	for _, c := range comments {
		if c.UserID != userID {
			continue
		}
		c.Text = sanitizeText(c.Text)
		c.Player = sanitizeText(c.Player)
		if c.Text == "" {
			return Comment{}, fmt.Errorf("model produced no comment for that player")
		}
		if cfg.toneFor(c.Player) == "mute" {
			return Comment{}, fmt.Errorf("%s is muted", c.Player)
		}
		now := w.deps.Now()
		c.At = now
		c.ExpiresAt = time.Time{} // never expires on a clock — replaced by the next pass
		w.cache.Upsert(c)
		if w.deps.SaveComment != nil {
			if err := w.deps.SaveComment(c); err != nil {
				w.logger.Printf("ai: persist regenerated comment for %s: %v", c.Player, err)
			}
		}
		if err := appendCommentLog(w.logPath, cfg.toneFor(c.Player), now, c); err != nil {
			w.logger.Printf("ai: log regenerated comment for %s: %v", c.Player, err)
		}
		w.mon.record(CommentAction{At: now, Player: c.Player, Text: c.Text, Outcome: "written"})
		w.logger.Printf("ai: regenerated comment for %s", c.Player)
		return c, nil
	}
	return Comment{}, fmt.Errorf("model didn't write a comment for that player")
}

// priorComments reads the current cached line per player (keyed by display name)
// so the next pass can be told what it said last time and write something fresh.
// Nil when there's no cache or it's empty (first fill) — the prompt then omits the
// prior-lines block entirely.
func (w *CommentWorker) priorComments() map[string]string {
	if w.cache == nil {
		return nil
	}
	cur := w.cache.All(w.deps.Now())
	if len(cur) == 0 {
		return nil
	}
	prior := make(map[string]string, len(cur))
	for _, c := range cur {
		if c.Player == "" || c.Text == "" {
			continue
		}
		prior[c.Player] = c.Text
	}
	return prior
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
	combined := w.deps.DerivedNotes()
	if w.deps.PendingDigests == nil || w.deps.AddDerivedNote == nil || w.cmt == nil {
		return combined
	}
	pending, err := w.deps.PendingDigests()
	if err != nil {
		w.logger.Printf("ai: pending digests: %v", err)
		return combined
	}
	// One note per finished game (the seam returns the games still missing a note).
	wrote := false
	for _, data := range pending {
		if len(data.Matches) == 0 {
			continue
		}
		text, err := w.cmt.DigestResults(ctx, data, cfg)
		if err != nil {
			w.logger.Printf("ai: derived-note digest (match %d): %v", data.MatchID, err)
			continue
		}
		text = sanitizeText(text)
		if text == "" {
			continue
		}
		if err := w.deps.AddDerivedNote(data.MatchID, text); err != nil {
			w.logger.Printf("ai: save derived note (match %d): %v", data.MatchID, err)
			continue
		}
		wrote = true
	}
	if wrote {
		// Re-read so the prompt sees the freshly appended stories.
		combined = w.deps.DerivedNotes()
	}
	return combined
}

// refreshAutoRivalries asks BETanIA to update her self-managed rivalry set from the
// current standings + the story so far, persists it, and returns the merged rivalry
// set (admin + auto + pinned) for THIS pass to use. Best-effort: any fault logs and
// falls back to the rivalries cfg already carries (admin + whatever was stored). A
// no-op when the seams aren't wired.
func (w *CommentWorker) refreshAutoRivalries(ctx context.Context, cfg CommentConfig, history []RoundStanding) []Rivalry {
	if w.cmt == nil || w.deps.AutoRivalries == nil || w.deps.SetAutoRivalries == nil || w.deps.Config == nil {
		return cfg.Rivalries
	}
	desired, err := w.cmt.UpdateRivalries(ctx, history, cfg.DerivedNotes, w.deps.AutoRivalries(), cfg)
	if err != nil {
		w.logger.Printf("ai: update rivalries: %v", err)
		return cfg.Rivalries
	}
	clean := make([]Rivalry, 0, len(desired))
	for _, r := range desired {
		// Notes are untrusted model output rendered into terminals — same boundary as
		// comments. Names are matched to real users service-side; drop blanks here.
		note := sanitizeText(r.Note)
		if r.A == "" || r.B == "" {
			continue
		}
		clean = append(clean, Rivalry{A: r.A, B: r.B, Note: note})
	}
	if err := w.deps.SetAutoRivalries(clean); err != nil {
		w.logger.Printf("ai: save auto rivalries: %v", err)
		return cfg.Rivalries
	}
	// Re-read so the merged set (admin + pinned + freshly stored auto) feeds this pass.
	return w.deps.Config().Rivalries
}

// CompactNotes fuses the per-game derived-notes diary into ONE consolidated
// narrative (weighting recent games most), sanitized for terminal output. The
// service seam for the admin "compact derived" action — it owns reading the diary
// and storing the result; the worker only runs the model call. Returns "" (no
// change) when there's fewer than two entries or the call fails.
func (w *CommentWorker) CompactNotes(ctx context.Context, notes []string) (string, error) {
	if w.cmt == nil || len(notes) < 2 {
		return "", nil
	}
	text, err := w.cmt.CompactNotes(ctx, notes, w.deps.Config())
	if err != nil {
		return "", err
	}
	return sanitizeText(text), nil
}

// CompactHouseNotes fuses the admin's free-text house notes into ONE consolidated
// note (a lossless merge that preserves every distinct fact, not a recency-weighted
// narrative), sanitized for terminal output. The service seam for the admin "compact
// house notes" action — the service owns reading/storing; the worker only runs the
// model call. Returns "" (no change) when there's fewer than two notes or the call
// fails.
func (w *CommentWorker) CompactHouseNotes(ctx context.Context, notes []string) (string, error) {
	if w.cmt == nil || len(notes) < 2 {
		return "", nil
	}
	text, err := w.cmt.CompactHouseNotes(ctx, notes, w.deps.Config())
	if err != nil {
		return "", err
	}
	return sanitizeText(text), nil
}
