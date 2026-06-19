package live

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"bethoven/internal/clock"
	"bethoven/internal/models"
)

// FinalizeFunc records the official result for a match that the feed reports as
// finished. The Poller calls it once, on the transition into the finished state.
type FinalizeFunc func(matchID int64, a, b int) error

// MatchesFunc returns the current set of stored matches to resolve events against.
type MatchesFunc func() ([]models.Match, error)

// Poller periodically fetches live events and writes resolved scores into a
// Cache. It depends only on small function seams (matches, finalize) plus a
// clock, so it never imports the service and stays trivially testable.
type Poller struct {
	provider Provider
	cache    *Cache
	matches  MatchesFunc
	finalize FinalizeFunc
	clk      clock.Clock
	interval time.Duration
	aliases  map[string]string // normalized name -> canonical normalized name
	logger   *log.Logger
}

// NewPoller wires a poller. interval is the time between fetches.
func NewPoller(p Provider, c *Cache, matches MatchesFunc, finalize FinalizeFunc, clk clock.Clock, interval time.Duration) *Poller {
	return &Poller{
		provider: p,
		cache:    c,
		matches:  matches,
		finalize: finalize,
		clk:      clk,
		interval: interval,
		aliases:  defaultAliases(),
		logger:   log.Default(),
	}
}

// Run polls until ctx is cancelled. It fires once immediately, then on each tick.
func (p *Poller) Run(ctx context.Context) {
	t := time.NewTicker(p.interval)
	defer t.Stop()
	p.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.poll(ctx)
		}
	}
}

// poll runs a single fetch/resolve/apply pass. It never panics out: the feed is
// untrusted, so any fault is logged and the server carries on with stale data.
func (p *Poller) poll(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Printf("live: recovered from panic in poll: %v", r)
		}
	}()

	matches, err := p.matches()
	if err != nil {
		p.logger.Printf("live: load matches: %v", err)
		return
	}

	// Live/just-finished matches are always "today"; fetch yesterday too so a
	// match that ended around a restart or near midnight UTC still finalizes.
	now := p.clk.Now().UTC()
	days := []time.Time{now, now.AddDate(0, 0, -1)}

	events, err := p.provider.Fetch(ctx, days)
	if err != nil {
		p.logger.Printf("live: fetch: %v", err)
		return
	}

	// Rebuild the snapshot from scratch each pass so stale matches expire.
	fresh := make(map[int64]Score)
	for _, ev := range events {
		if ev.State == StatePre {
			continue // nothing to show; never reveal an unstarted match
		}
		m, swapped, ok := p.resolve(ev, matches)
		if !ok {
			// %q escapes the untrusted feed names so they can't inject control
			// chars into the operator's log/terminal (same boundary as cleanClock).
			p.logger.Printf("live: unresolved event %q v %q", ev.Home, ev.Away)
			continue
		}
		a, b := ev.HomeScore, ev.AwayScore
		if swapped {
			a, b = b, a
		}
		// Only in-play matches belong in the live cache; finished ones are
		// settled to the DB below and read from the authoritative result. (If the
		// feed still reports StateIn after we've finalized, it's harmless: the
		// service filters it downstream — overlayLive and the leaderboard fold both
		// skip m.Finished — so the authoritative result always wins.)
		if ev.State == StateIn {
			// Odds and key-event text reference team names directly, so copy straight
			// through — no home/away orientation needed.
			fresh[m.ID] = Score{A: a, B: b, State: StateIn, Minute: ev.Minute, Clock: ev.Clock, Phase: ev.Phase, Odds: ev.Odds, Events: ev.KeyEvents}
		}
		if ev.State == StatePost && !m.Finished {
			if err := p.finalize(m.ID, a, b); err != nil {
				p.logger.Printf("live: finalize match %d: %v", m.ID, err)
			}
		}
	}
	p.cache.Replace(fresh)
}

// resolve maps an event to a stored match by canonical team pair + a generous
// kickoff-date tolerance. Returns the match, whether home/away is swapped
// relative to our TeamA/TeamB, and whether a match was found.
//
// No memoization: the match list is tiny (~104) and only a handful of events
// poll at a time, so a full scan is cheap — and it keeps the date guard always
// active, so a group pairing that recurs in the knockouts binds to the right
// fixture instead of being stuck on the first one it ever saw.
func (p *Poller) resolve(ev Event, matches []models.Match) (models.Match, bool, bool) {
	ch, ca := p.canonical(ev.Home), p.canonical(ev.Away)
	key := pairKey(ch, ca)

	for _, m := range matches {
		if pairKey(p.canonical(m.TeamA), p.canonical(m.TeamB)) != key {
			continue
		}
		// Date guard: tolerate tz/midnight skew, but reject a far-apart rematch.
		if !ev.Date.IsZero() {
			if d := ev.Date.Sub(m.StartsAt.UTC()); d > 36*time.Hour || d < -36*time.Hour {
				continue
			}
		}
		return m, p.canonical(m.TeamA) != ch, true
	}
	return models.Match{}, false, false
}

// canonical normalizes a team name and applies the alias map, so "United States"
// and "USA" collapse to the same key.
func (p *Poller) canonical(name string) string {
	n := normalize(name)
	if c, ok := p.aliases[n]; ok {
		return c
	}
	return n
}

// normalize folds accents then lowercases and strips everything but
// letters/digits, so spacing, punctuation, case, and diacritics never break a
// match (e.g. ESPN's "Türkiye" matches our "Turkey" once aliased).
func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(foldAccents(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// foldAccents strips diacritics by NFD-decomposing and dropping the combining
// marks, so "Türkiye" -> "Turkiye", "Curaçao" -> "Curacao".
func foldAccents(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, r) { // Mn = nonspacing combining mark
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// pairKey is an order-independent key for a pair of canonical names.
func pairKey(a, b string) string {
	p := []string{a, b}
	sort.Strings(p)
	return p[0] + "|" + p[1]
}

// defaultAliases maps a few common ESPN spellings to a shared canonical form.
// The durable fix is to align fixtures.json team names with ESPN's; this just
// covers residual mismatches. Keys and values are already normalized.
func defaultAliases() map[string]string {
	return map[string]string{
		"unitedstates":         "usa",
		"us":                   "usa",
		"southkorea":           "korearepublic",
		"korea":                "korearepublic",
		"bosniaandherzegovina": "bosniaherzegovina",
		"czechrepublic":        "czechia",
		"ivorycoast":           "cotedivoire",
		"turkey":               "turkiye", // ESPN: "Türkiye" (accent-folded)
		"drcongo":              "congodr", // ESPN: "Congo DR"
	}
}
