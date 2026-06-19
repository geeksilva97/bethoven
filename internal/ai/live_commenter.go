package ai

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

// liveCommentPassTimeout bounds one live-comment generation (a single model call,
// no web search). Short — it runs while a match is in play and must stay current.
const liveCommentPassTimeout = 90 * time.Second

// liveHistoryMax caps how many recent lines we keep (and feed back to the model so
// it doesn't repeat itself). A short game-long memory, dropped when the game ends.
const liveHistoryMax = 5

// Live-cadence constants. The worker ticks often (so it reacts to a goal quickly),
// regenerates when the situation CHANGES or a heartbeat elapses, and never sooner
// than the floor apart (so a flurry of goals can't spam the API).
const (
	liveTick  = 30 * time.Second  // how often we re-check the live situation
	liveFloor = 120 * time.Second // minimum spacing between two generations
)

// LivePickInfo is one player's pick on an in-play match plus the provisional points
// it is currently earning — grounding for "who's nailing it" lines.
type LivePickInfo struct {
	Player       string `json:"player"`
	PredA, PredB int    `json:"-"`
	Pred         string `json:"pred"`   // "2-1", filled from PredA/PredB for the model
	LivePoints   int    `json:"points"` // provisional points right now
}

// LiveEventInfo is one key in-play moment (goal/card) for the live commenter. Text
// carries the scorer/player name (the feed's structured athlete refs are empty for
// this competition), so it's the source of truth for "who scored". Sanitized at the
// feed boundary before it ever reaches here.
type LiveEventInfo struct {
	Clock string `json:"clock,omitempty"`
	Type  string `json:"type"`
	Text  string `json:"text"`
}

// LiveMatchInfo is one in-play match's situation for the live commenter.
type LiveMatchInfo struct {
	TeamA  string          `json:"team_a"`
	TeamB  string          `json:"team_b"`
	ScoreA int             `json:"score_a"`
	ScoreB int             `json:"score_b"`
	Clock  string          `json:"clock"`
	Phase  string          `json:"phase,omitempty"` // "halftime"/"extra_time"/"penalties"; absent for ordinary play
	Odds   string          `json:"odds,omitempty"`  // sanitized pre-match odds; may be empty
	Events []LiveEventInfo `json:"key_events,omitempty"`
	Picks  []LivePickInfo  `json:"picks,omitempty"`
}

// LiveMover is a player whose standing is shifting because of the in-play results:
// RankDelta is places gained(+)/lost(-) vs settled points, LivePoints the provisional gain.
type LiveMover struct {
	Player     string `json:"player"`
	RankDelta  int    `json:"rank_delta"`
	LivePoints int    `json:"live_points"`
}

// LiveStanding is one player's overall position for the halftime leaderboard
// snapshot: enough (position + total) to reason about who's closing on whom.
type LiveStanding struct {
	Player     string `json:"player"`
	Position   int    `json:"position"`
	Total      int    `json:"total"`
	LivePoints int    `json:"live_points,omitempty"` // provisional gain from the in-play match
}

// LiveSituation is the snapshot the live commenter reasons over: the matches in
// play (score, clock, odds, who picked what) and the notable leaderboard movers.
// Standings is populated ONLY at halftime, when the commentary pivots from the
// match to the pool dynamics (gaps, who's climbing, who's stuck) — it stays empty
// during open play to keep those prompts lean.
type LiveSituation struct {
	Matches   []LiveMatchInfo `json:"matches"`
	Movers    []LiveMover     `json:"movers,omitempty"`
	Standings []LiveStanding  `json:"standings,omitempty"`
}

// phaseHalftime is OUR controlled label for the interval (mirrors live.PhaseHalftime);
// compared as a literal so this package needn't import internal/live.
const phaseHalftime = "halftime"

// halftimeFocus reports whether the commentary should pivot to leaderboard dynamics:
// there is at least one live match and every live match is at the interval. During
// open play (or ET/penalties) it's false and the normal play-by-play prompt is used.
func halftimeFocus(sit LiveSituation) bool {
	if len(sit.Matches) == 0 {
		return false
	}
	for _, m := range sit.Matches {
		if m.Phase != phaseHalftime {
			return false
		}
	}
	return true
}

// LiveCommentDeps are the service seams the live-comment worker needs. Functions,
// not the service, so the package stays import-cycle free (mirrors CommentDeps).
type LiveCommentDeps struct {
	Situation func() (LiveSituation, bool, error) // bool reports whether anything is live
	Config    func() CommentConfig                // tone + admin prompt override
	Now       func() time.Time
}

// LiveCommenter writes a single general play-by-play line for the current live
// situation, aware of the recent lines so it doesn't repeat itself. The concrete
// implementation is *AnthropicCommenter; tests use a fake.
type LiveCommenter interface {
	WriteLiveComment(ctx context.Context, sit LiveSituation, recent []string, cfg CommentConfig) (string, error)
}

// LiveCommentCache holds BETanIA's CURRENT live-commentary line in memory, plus a
// short rolling history of recent lines and the last situation signature. The
// worker writes it; the service's LiveCommentSource port reads it. Nothing is
// persisted, and the whole thing is cleared when no match is live — a live line
// is throwaway by design. Concurrency-safe.
type LiveCommentCache struct {
	mu        sync.RWMutex
	text      string
	expiresAt time.Time
	sig       string
	history   []string
}

// NewLiveCommentCache returns an empty cache.
func NewLiveCommentCache() *LiveCommentCache { return &LiveCommentCache{} }

// Current returns the live line if it is still fresh, else "" (implements
// service.LiveCommentSource). now is supplied so the read uses the service clock.
func (c *LiveCommentCache) Current(now time.Time) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.text == "" || (!c.expiresAt.IsZero() && now.After(c.expiresAt)) {
		return ""
	}
	return c.text
}

// set records a freshly written line: it becomes current, joins the history ring
// (capped), and its signature is remembered so an unchanged situation isn't
// regenerated before the heartbeat.
func (c *LiveCommentCache) set(text, sig string, expiresAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.text, c.sig, c.expiresAt = text, sig, expiresAt
	c.history = append(c.history, text)
	if len(c.history) > liveHistoryMax {
		c.history = c.history[len(c.history)-liveHistoryMax:]
	}
}

// clear wipes everything — called when nothing is live, so the game's lines are
// discarded the moment it ends (and don't bleed into the next match).
func (c *LiveCommentCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.text, c.sig, c.expiresAt, c.history = "", "", time.Time{}, nil
}

// snapshot returns the last signature and a copy of the recent-line history, for
// the worker's change-detection and the "don't repeat" prompt input.
func (c *LiveCommentCache) snapshot() (sig string, history []string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	h := make([]string, len(c.history))
	copy(h, c.history)
	return c.sig, h
}

// LiveCommentWorker generates BETanIA's top-of-leaderboard live commentary. While
// at least one match is in play it regenerates the single line on change or on a
// heartbeat (whichever comes first, floored so goals can't spam the API), and it
// clears the cache entirely once nothing is live. Mirrors CommentWorker, but with
// a fast cadence and a self-clearing, throwaway cache.
type LiveCommentWorker struct {
	deps      LiveCommentDeps
	cmt       LiveCommenter
	cache     *LiveCommentCache
	heartbeat time.Duration // regenerate at least this often while live
	ttl       time.Duration // how long a written line stays current
	logPath   string
	logger    *log.Logger
	lastGen   time.Time
}

// NewLiveCommentWorker wires a live-comment worker. heartbeat is the longest a
// quiet (e.g. 0-0) game waits between fresh lines; the displayed line lives for
// twice that, so a briefly-stalled worker doesn't blank the board mid-game.
func NewLiveCommentWorker(deps LiveCommentDeps, cmt LiveCommenter, cache *LiveCommentCache, heartbeat time.Duration, logPath string) *LiveCommentWorker {
	// A heartbeat below the regeneration floor is meaningless and would make the
	// TTL (2×heartbeat) shorter than the floor — the line would flash then blank
	// for the rest of the floor window. Clamp so a misconfig can't do that.
	if heartbeat < liveFloor {
		heartbeat = liveFloor
	}
	return &LiveCommentWorker{
		deps:      deps,
		cmt:       cmt,
		cache:     cache,
		heartbeat: heartbeat,
		ttl:       2 * heartbeat,
		logPath:   logPath,
		logger:    log.Default(),
	}
}

// Run generates live commentary until ctx is cancelled. It checks on a fast tick
// (so a goal is reflected quickly) and decides each tick whether to regenerate.
func (w *LiveCommentWorker) Run(ctx context.Context) {
	t := time.NewTicker(liveTick)
	defer t.Stop()
	w.pass(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.pass(ctx)
		}
	}
}

// pass evaluates the live situation and regenerates the line when warranted. Never
// panics out: a fault is logged and the worker carries on next tick.
func (w *LiveCommentWorker) pass(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Printf("ai: recovered from panic in live-comment pass: %v", r)
		}
	}()

	sit, isLive, err := w.deps.Situation()
	if err != nil {
		w.logger.Printf("ai: live situation: %v", err)
		return
	}
	if !isLive {
		// Game over (or none in play): throw the line + history away.
		w.cache.clear()
		w.lastGen = time.Time{}
		return
	}

	now := w.deps.Now()
	prevSig, history := w.cache.snapshot()
	sig := liveSignature(sit)

	first := w.lastGen.IsZero()
	changed := sig != prevSig
	heartbeatDue := !first && now.Sub(w.lastGen) >= w.heartbeat
	if !first && !changed && !heartbeatDue {
		return // nothing new and not yet time for a heartbeat refresh
	}
	// Floor: never regenerate two lines closer than liveFloor apart. A change that
	// arrives inside the floor is picked up on a later tick once it elapses (the
	// cached signature is still stale, so `changed` stays true).
	if !first && now.Sub(w.lastGen) < liveFloor {
		return
	}

	// The live line addresses the pool in general, so cfg.Self (BETanIA's own name,
	// used only for the per-player first-person line) is deliberately left unset.
	cfg := w.deps.Config()

	pctx, cancel := context.WithTimeout(ctx, liveCommentPassTimeout)
	defer cancel()

	text, err := w.cmt.WriteLiveComment(pctx, sit, history, cfg)
	if err != nil {
		w.logger.Printf("ai: write live comment: %v", err)
		return
	}
	// Sanitize at the cache/log boundary — untrusted model output rendered into
	// every terminal, the same ANSI-injection boundary as display names.
	text = sanitizeText(text)
	if text == "" {
		return
	}
	w.cache.set(text, sig, now.Add(w.ttl))
	w.lastGen = now
	if err := appendLiveCommentLog(w.logPath, now, text); err != nil {
		w.logger.Printf("ai: log live comment: %v", err)
	}
	w.logger.Printf("ai: wrote live comment (%d live match(es))", len(sit.Matches))
}

// liveSignature is a deterministic fingerprint of the parts of the situation that
// should trigger a fresh line: each match's score, and each mover's standing
// shift. The clock is deliberately EXCLUDED — it ticks every minute and would
// force a regeneration on every pass; the heartbeat covers "nothing changed but
// time passed".
func liveSignature(sit LiveSituation) string {
	var b strings.Builder
	ms := make([]string, 0, len(sit.Matches))
	for _, m := range sit.Matches {
		// Score change triggers a fresh line; also fold in the key-event tail so a
		// card (which doesn't move the score) still prompts a new take promptly.
		ev := ""
		if n := len(m.Events); n > 0 {
			last := m.Events[n-1]
			ev = fmt.Sprintf("|%d:%s:%s", n, last.Clock, last.Type)
		}
		// Phase is part of the signature so the whistle for halftime (or full-time
		// pending) prompts a fresh line even though the score didn't move.
		ms = append(ms, fmt.Sprintf("%s%d-%d%s@%s%s", m.TeamA, m.ScoreA, m.ScoreB, m.TeamB, m.Phase, ev))
	}
	sort.Strings(ms)
	b.WriteString(strings.Join(ms, "|"))
	b.WriteString("#")
	mv := make([]string, 0, len(sit.Movers))
	for _, m := range sit.Movers {
		mv = append(mv, fmt.Sprintf("%s:%d:%d", m.Player, m.RankDelta, m.LivePoints))
	}
	sort.Strings(mv)
	b.WriteString(strings.Join(mv, ","))
	return b.String()
}
