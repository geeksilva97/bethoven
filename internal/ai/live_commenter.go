package ai

import (
	"context"
	"encoding/json"
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

// Director-driven roast regeneration limits — so "she decides when to refresh a
// player's roast" can't spam the (heavier, per-player-model) regeneration path.
const (
	maxRegenPerPass   = 3                // at most this many roast regens requested per director pass
	commentRegenFloor = 15 * time.Minute // don't regenerate the SAME player's roast more often than this
	liveRegenTimeout  = 3 * time.Minute  // bounds one background roast regeneration
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

// LiveStanding is one player's overall position for the live commentary's
// leadership-race angle: position + total (so gaps are groundable) plus any
// provisional gain from the in-play match.
type LiveStanding struct {
	Player     string `json:"player"`
	Position   int    `json:"position"`
	Total      int    `json:"total"`
	LivePoints int    `json:"live_points,omitempty"`
}

// LiveUpcoming is a match about to kick off — grounding for a "game about to
// start" hype line. MinutesToKO is whole minutes until kickoff (>0).
type LiveUpcoming struct {
	TeamA       string `json:"team_a"`
	TeamB       string `json:"team_b"`
	Stage       string `json:"stage,omitempty"`
	MinutesToKO int    `json:"minutes_to_kickoff"`
}

// LiveSettled is a match that JUST finished — grounding for a result reaction.
// Score is the regulation 90' result ("2-1"); Picks reuses LivePickInfo, whose
// "points" field here is the FINAL points each player scored on the game.
type LiveSettled struct {
	TeamA string         `json:"team_a"`
	TeamB string         `json:"team_b"`
	Score string         `json:"score"`
	Stage string         `json:"stage,omitempty"`
	Picks []LivePickInfo `json:"picks,omitempty"`
}

// LiveSituation is the snapshot the director reasons over: the matches in play
// (score, clock, odds, who picked what), matches about to kick off, matches that
// just finished (with how the pool bet them), the notable leaderboard movers, and
// the overall standings so the line can riff on the title race / shrinking gaps
// instead of fixating on the one standout pick. At halftime the prompt pivots
// entirely to those pool dynamics (see halftimeFocus); with nothing in play it
// pivots to the upcoming/just-finished slate.
type LiveSituation struct {
	Matches   []LiveMatchInfo `json:"matches"`
	Upcoming  []LiveUpcoming  `json:"upcoming,omitempty"`
	Settled   []LiveSettled   `json:"settled,omitempty"`
	Movers    []LiveMover     `json:"movers,omitempty"`
	Standings []LiveStanding  `json:"standings,omitempty"`
}

// hasContent reports whether there's anything for the director to talk about: a
// match in play, one about to start, or one just finished.
func (s LiveSituation) hasContent() bool {
	return len(s.Matches) > 0 || len(s.Upcoming) > 0 || len(s.Settled) > 0
}

// inPlay reports whether at least one match is actually live (vs only upcoming /
// just-settled), which selects the play-by-play prompt over the pregame one.
func (s LiveSituation) inPlay() bool { return len(s.Matches) > 0 }

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

// LiveCommentDeps are the service seams the director needs. Functions, not the
// service, so the package stays import-cycle free (mirrors CommentDeps).
type LiveCommentDeps struct {
	Situation func() (LiveSituation, bool, error) // bool reports whether there's anything to talk about
	Config    func() CommentConfig                // tone + admin prompt override + current mood
	Now       func() time.Time
	// DerivedNotes returns BETanIA's auto "story of the game" summaries for the
	// latest finished matches (most recent last), so the live line can carry the
	// narrative across sequential games — e.g. reference the wreck that just ended
	// while the next match is in play. Optional — nil ⇒ no past-game context (the
	// live line is still written, grounded only in the current situation).
	DerivedNotes func() string
	// SetMood persists BETanIA's freshly chosen mood (one of MoodValues). Optional —
	// nil ⇒ mood never evolves (the line is still written). The service validates.
	SetMood func(mood string) error
	// RegenComment regenerates ONE player's per-player leaderboard roast, by display
	// name — the director's "this player's roast is stale now" decision. Optional —
	// nil ⇒ the director never triggers roast regenerations. Runs on the per-player
	// path (its own model), not the director's; the worker rate-limits the calls.
	RegenComment func(ctx context.Context, name string) error
}

// LiveOutput is the director's decision for one pass: the line to show (empty ⇒
// stay silent), BETanIA's updated mood (empty ⇒ leave it unchanged), and the
// players whose per-player roast she judges stale enough to rewrite now.
type LiveOutput struct {
	Comment string
	Mood    string
	Regen   []string // exact player display names to regenerate; usually empty
}

// LiveCommenter writes a single general line for the current situation (in-play,
// upcoming, or just-finished) plus an updated mood, aware of the recent lines so
// it doesn't repeat itself. The concrete implementation is *AnthropicCommenter;
// tests use a fake.
type LiveCommenter interface {
	WriteLiveComment(ctx context.Context, sit LiveSituation, recent []string, cfg CommentConfig) (LiveOutput, error)
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

// markSig records the situation signature as handled WITHOUT changing the
// displayed line — used when the director decides to stay silent, so the same
// situation isn't re-evaluated every tick (the prior line, if any, still expires
// on its own TTL).
func (c *LiveCommentCache) markSig(sig string) {
	c.mu.Lock()
	c.sig = sig
	c.mu.Unlock()
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

// livePersistState is the serialized form of the cache. The service stores the
// JSON in the settings table (KV "live_comment_state") so a version swap mid-game
// keeps BETanIA's current line + anti-repeat memory. The ai package only marshals
// to/from this string — it never touches the DB itself (main bridges to service),
// keeping the no-import-cycle rule intact, like CommentConfig's seams.
type livePersistState struct {
	Text      string    `json:"text"`
	Sig       string    `json:"sig"`
	ExpiresAt time.Time `json:"expires_at"`
	History   []string  `json:"history"`
}

// SnapshotJSON returns the cache state as a JSON string for the service to persist
// (settings KV). Empty string when there's nothing worth saving.
func (c *LiveCommentCache) SnapshotJSON() string {
	c.mu.RLock()
	st := livePersistState{Text: c.text, Sig: c.sig, ExpiresAt: c.expiresAt, History: append([]string(nil), c.history...)}
	c.mu.RUnlock()
	if st.Text == "" && len(st.History) == 0 {
		return ""
	}
	b, err := json.Marshal(st)
	if err != nil {
		return ""
	}
	return string(b)
}

// LoadJSON restores cache state from a JSON string previously produced by
// SnapshotJSON (the service hands it back at boot), so a freshly started process
// resumes the live line + history instead of a blank board. An empty or malformed
// string is ignored. If the snapshot is stale — the game is over — the worker's
// next pass sees nothing live and clears it (self-correcting), and Current() hides
// the line once ExpiresAt passes.
func (c *LiveCommentCache) LoadJSON(s string) {
	if s == "" {
		return
	}
	var st livePersistState
	if err := json.Unmarshal([]byte(s), &st); err != nil {
		return
	}
	c.mu.Lock()
	c.text, c.sig, c.expiresAt, c.history = st.Text, st.Sig, st.ExpiresAt, st.History
	c.mu.Unlock()
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
	self      string        // BETanIA's own display name, so she can ground her mood in her own row
	heartbeat time.Duration // regenerate at least this often while live
	ttl       time.Duration // how long a written line stays current
	logPath   string
	logger    *log.Logger
	lastGen   time.Time
	// lastRegen tracks when each player's roast was last regenerated at the
	// director's request, to enforce commentRegenFloor. Only the Run goroutine
	// touches it (pass is sequential), so no lock is needed.
	lastRegen map[string]time.Time
	// focusIdx rotates the forced focus angle so consecutive live lines cover
	// DIFFERENT pool dynamics instead of fixating on one hot player. Only the Run
	// goroutine touches it (pass is sequential), so no lock is needed.
	focusIdx int
}

// liveFocus is one angle in the director's rotation: the directive text fed to the
// prompt, and a guard reporting whether the situation has the data to back it.
type liveFocus struct {
	text string
	has  func(sit LiveSituation, cfg CommentConfig) bool
}

// liveFocusRotation forces intercalation: each generated live line is assigned the
// NEXT angle whose data exists, so a dominant story (a hot streak) can't monopolise
// the headline. The model still writes the line freely within the chosen angle.
var liveFocusRotation = []liveFocus{
	{"the TITLE RACE at the top — who leads and who's closing the gap (ground any gap in \"standings\" totals)", func(s LiveSituation, _ CommentConfig) bool { return len(s.Standings) >= 2 }},
	{"someone FAR FROM THE LEAD — a player near the BOTTOM of \"standings\", or stuck on zero live points; give the strugglers a moment", func(s LiveSituation, _ CommentConfig) bool { return len(s.Standings) >= 3 }},
	{"a RIVALRY from the rivalries list — how the two are faring against each other right now", func(_ LiveSituation, c CommentConfig) bool { return len(c.Rivalries) > 0 }},
	{"a CLIMBER or FALLER on live points (\"movers\") — someone whose position is swinging", func(s LiveSituation, _ CommentConfig) bool { return len(s.Movers) > 0 }},
	{"who NAILED or WHIFFED a live scoreline (\"picks\") — pick a player you have NOT featured recently", func(s LiveSituation, _ CommentConfig) bool { return len(s.Matches) > 0 }},
}

// pickFocus returns the next focus directive whose data is present, starting from
// focusIdx, plus the index to commit AFTER a non-empty line is produced (so a
// silent pass doesn't burn an angle). Returns "" if nothing has data.
func (w *LiveCommentWorker) pickFocus(sit LiveSituation, cfg CommentConfig) (string, int) {
	n := len(liveFocusRotation)
	for i := 0; i < n; i++ {
		idx := (w.focusIdx + i) % n
		if liveFocusRotation[idx].has(sit, cfg) {
			return liveFocusRotation[idx].text, (idx + 1) % n
		}
	}
	return "", w.focusIdx
}

// NewLiveCommentWorker wires the director. self is BETanIA's display name (so she
// can find her own standings row when updating her mood). heartbeat is the longest
// a quiet (e.g. 0-0) game waits between fresh lines; the displayed line lives for
// twice that, so a briefly-stalled worker doesn't blank the board mid-game.
func NewLiveCommentWorker(deps LiveCommentDeps, cmt LiveCommenter, cache *LiveCommentCache, self string, heartbeat time.Duration, logPath string) *LiveCommentWorker {
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
		self:      self,
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

	sit, active, err := w.deps.Situation()
	if err != nil {
		w.logger.Printf("ai: live situation: %v", err)
		return
	}
	if !active {
		// Nothing in play, upcoming, or just-finished: throw the line + history away.
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

	// The line addresses the pool in general, but Self lets her find her own row to
	// ground her mood ("am I winning?"); it isn't used to write the line itself.
	cfg := w.deps.Config()
	cfg.Self = w.self
	// Past-game context: BETanIA's own summaries of the latest finished matches, so
	// the live line can thread the narrative across sequential games (the prior
	// match's story isn't in the live situation once it's cleared).
	if w.deps.DerivedNotes != nil {
		cfg.DerivedNotes = w.deps.DerivedNotes()
	}
	// Forced focus angle for THIS line — rotated so the commentary can't fixate on
	// one player. Committed (focusIdx advanced) only after a non-empty line, so a
	// silent pass doesn't waste an angle.
	focus, focusNext := w.pickFocus(sit, cfg)
	cfg.LiveFocus = focus

	pctx, cancel := context.WithTimeout(ctx, liveCommentPassTimeout)
	defer cancel()

	out, err := w.cmt.WriteLiveComment(pctx, sit, history, cfg)
	if err != nil {
		w.logger.Printf("ai: write live comment: %v", err)
		return
	}

	// Mark this situation handled regardless of whether she spoke, so we don't
	// re-ask every tick until something actually changes (or the heartbeat fires).
	w.lastGen = now

	// Apply the freshly chosen mood (best-effort; the service validates the value).
	if w.deps.SetMood != nil {
		if m := NormalizeMood(out.Mood); m != "" {
			if err := w.deps.SetMood(m); err != nil {
				w.logger.Printf("ai: set mood: %v", err)
			}
		}
	}

	// Her per-player decision: regenerate the roasts she judged stale (rate-limited).
	w.regenStaleRoasts(ctx, now, out.Regen)

	// Sanitize at the cache/log boundary — untrusted model output rendered into
	// every terminal, the same ANSI-injection boundary as display names.
	text := sanitizeText(out.Comment)
	if text == "" {
		// She chose to stay silent: record the signature so the same situation isn't
		// re-evaluated next tick, but leave the prior line to expire on its own.
		w.cache.markSig(sig)
		return
	}
	w.cache.set(text, sig, now.Add(w.ttl))
	w.focusIdx = focusNext // she spoke — advance the rotation so the next line shifts angle
	if err := appendLiveCommentLog(w.logPath, now, text); err != nil {
		w.logger.Printf("ai: log live comment: %v", err)
	}
	w.logger.Printf("ai: wrote live comment (live=%d upcoming=%d settled=%d)", len(sit.Matches), len(sit.Upcoming), len(sit.Settled))
}

// regenStaleRoasts fires the director's per-player roast regenerations — the
// "this player's leaderboard line is stale now" decision. Each runs in the
// background (the regeneration is a couple of model calls on the per-player path,
// which must not block the fast director tick), capped per pass and floored per
// player so she can't spam it.
func (w *LiveCommentWorker) regenStaleRoasts(ctx context.Context, now time.Time, names []string) {
	if w.deps.RegenComment == nil || len(names) == 0 {
		return
	}
	if w.lastRegen == nil {
		w.lastRegen = make(map[string]time.Time)
	}
	fired := 0
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if t, ok := w.lastRegen[name]; ok && now.Sub(t) < commentRegenFloor {
			continue // refreshed too recently — skip
		}
		w.lastRegen[name] = now
		fired++
		n := name
		go func() {
			rctx, cancel := context.WithTimeout(ctx, liveRegenTimeout)
			defer cancel()
			if err := w.deps.RegenComment(rctx, n); err != nil {
				w.logger.Printf("ai: director-requested roast regen %q: %v", n, err)
			}
		}()
		if fired >= maxRegenPerPass {
			break
		}
	}
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

	// Upcoming: react once when a match enters the window ("soon") and once when it's
	// about to start ("imminent"). Bucketed so the per-minute countdown doesn't churn
	// the signature (the same reason the live clock is excluded above).
	b.WriteString("#")
	ups := make([]string, 0, len(sit.Upcoming))
	for _, u := range sit.Upcoming {
		bucket := "soon"
		if u.MinutesToKO <= 10 {
			bucket = "imminent"
		}
		ups = append(ups, fmt.Sprintf("%s-%s:%s", u.TeamA, u.TeamB, bucket))
	}
	sort.Strings(ups)
	b.WriteString(strings.Join(ups, ","))

	// Settled: a fresh result (teams + score) is a new thing to react to.
	b.WriteString("#")
	st := make([]string, 0, len(sit.Settled))
	for _, s := range sit.Settled {
		st = append(st, fmt.Sprintf("%s%s-%s", s.TeamA, s.Score, s.TeamB))
	}
	sort.Strings(st)
	b.WriteString(strings.Join(st, ","))
	return b.String()
}
