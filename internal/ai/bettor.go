package ai

import (
	"context"
	"log"
	"strings"
	"time"

	"bethoven/internal/models"
)

// perMatchTimeout bounds one prediction (web search + model) so a hung call can't
// stall the whole pass. Web search dominates latency, so this is generous; the
// search-round budget (maxWebSearches) is the primary lever that keeps picks fast.
const perMatchTimeout = 4 * time.Minute

// Deps are the service seams the Bettor needs. Passing functions (not the service
// itself) keeps this package free of an import cycle, mirroring live.MatchesFunc.
type Deps struct {
	Fixtures func() ([]models.Match, error)
	MyBets   func(userID int64) (map[int64]models.Bet, error)
	PlaceBet func(userID, matchID, predA, predB int64) error
	Now      func() time.Time
}

// Bettor is BETanIA's live worker: on a timer it researches and bets every
// upcoming, not-yet-bet match through the service (so the kickoff lock applies).
// Copies the live.Poller shape.
type Bettor struct {
	deps      Deps
	pred      Predictor
	mon       *Monitor
	userID    int64
	interval  time.Duration
	logPath   string
	maxPerRun int           // 0 = no cap
	lookahead time.Duration // only bet matches kicking off within this window; 0 = no horizon
	logger    *log.Logger
	trigger   chan struct{} // manual "run now" requests (buffered, coalesced to 1)
}

// NewBettor wires a live bettor. interval is the gap between passes; lookahead
// bounds how far ahead it will bet (so it works the near-term slate, not the whole
// fixture list). lookahead should be >= interval so every match enters the window
// before it kicks off; 0 disables the horizon.
func NewBettor(deps Deps, pred Predictor, mon *Monitor, userID int64, interval time.Duration, logPath string, maxPerRun int, lookahead time.Duration) *Bettor {
	return &Bettor{
		deps:      deps,
		pred:      pred,
		mon:       mon,
		userID:    userID,
		interval:  interval,
		logPath:   logPath,
		maxPerRun: maxPerRun,
		lookahead: lookahead,
		logger:    log.Default(),
		trigger:   make(chan struct{}, 1),
	}
}

// Trigger requests an immediate pass (e.g. the admin "run now" button), without
// waiting for the next tick. Non-blocking: if a pass is already queued it returns
// false and coalesces — at most one extra pass is ever pending. Safe across
// goroutines (the worker drains the channel; this only ever fills it).
func (b *Bettor) Trigger() bool {
	select {
	case b.trigger <- struct{}{}:
		return true
	default:
		return false // a run is already queued
	}
}

// Run bets until ctx is cancelled. It fires once immediately, then on each tick
// or whenever a manual Trigger lands.
func (b *Bettor) Run(ctx context.Context) {
	t := time.NewTicker(b.interval)
	defer t.Stop()
	b.bet(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.bet(ctx)
		case <-b.trigger:
			b.bet(ctx)
		}
	}
}

// bet runs a single research-and-place pass. It never panics out: any fault is
// logged and the worker carries on next tick.
func (b *Bettor) bet(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Printf("ai: recovered from panic in bet: %v", r)
		}
	}()

	b.mon.markRun(b.deps.Now())

	matches, err := b.deps.Fixtures()
	if err != nil {
		b.logger.Printf("ai: load fixtures: %v", err)
		return
	}
	myBets, err := b.deps.MyBets(b.userID)
	if err != nil {
		b.logger.Printf("ai: load my bets: %v", err)
		return
	}

	now := b.deps.Now()
	placed := 0
	for _, m := range matches {
		if b.maxPerRun > 0 && placed >= b.maxPerRun {
			return
		}
		if _, ok := myBets[m.ID]; ok {
			b.mon.skip()
			continue
		}
		// Only genuinely upcoming matches — the same predicate PlaceBet enforces.
		if m.Finished || !now.Before(m.StartsAt) {
			b.mon.skip()
			continue
		}
		// Stay near the current date: don't bet games beyond the lookahead window.
		// Fixtures are chronological (ORDER BY starts_at), so the first upcoming match
		// past the horizon means every later one is too — stop the pass here. BETanIA
		// will pick them up on a future pass as their kickoff approaches.
		if b.lookahead > 0 && m.StartsAt.After(now.Add(b.lookahead)) {
			return
		}
		if ctx.Err() != nil {
			return
		}

		pctx, cancel := context.WithTimeout(ctx, perMatchTimeout)
		pred, err := b.pred.Predict(pctx, m)
		cancel()
		if err != nil {
			b.logger.Printf("ai: predict %s: %v", matchLabel(m), err)
			b.mon.record(Action{At: b.deps.Now(), Match: matchLabel(m), Outcome: "error", Err: err.Error()})
			continue
		}

		if err := b.deps.PlaceBet(b.userID, m.ID, int64(pred.ScoreA), int64(pred.ScoreB)); err != nil {
			b.logger.Printf("ai: bet %s: %v", matchLabel(m), err)
			// A "betting closed" error means the match kicked off between the filter
			// and the call (a benign race) — count it as locked, not a fault. We match
			// on the message rather than the sentinel to avoid importing the service.
			outcome := "error"
			if strings.Contains(err.Error(), "kicked off") {
				outcome = "locked"
			}
			b.mon.record(Action{At: b.deps.Now(), Match: matchLabel(m), Outcome: outcome, Err: err.Error()})
			continue
		}

		placed++
		now2 := b.deps.Now()
		if err := appendLog(b.logPath, "live", now2, m, pred); err != nil {
			b.logger.Printf("ai: log bet %s: %v", matchLabel(m), err)
		}
		b.mon.record(Action{
			At: now2, Match: matchLabel(m),
			Score:      scoreText(pred),
			Rationale:  pred.Rationale,
			Confidence: pred.Confidence,
			Outcome:    "placed",
		})
		b.logger.Printf("ai: bet %s %s (%s)", matchLabel(m), scoreText(pred), pred.Confidence)
	}
}
