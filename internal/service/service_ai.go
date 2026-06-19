package service

import (
	"errors"
	"sort"

	"bethoven/internal/ai"
	"bethoven/internal/db"
	"bethoven/internal/models"
)

// AIMonitor is the optional observability port for BETanIA's live worker. The
// concrete implementation (ai.Monitor) is written by the worker and read here for
// the admin panel. nil is valid and means "BETanIA not running" — every read
// returns ErrAIOff. Mirrors the LiveStore / AnalyticsSink ports.
type AIMonitor interface {
	Status() ai.Status
	Activity(limit int) []ai.Action
}

// ErrAIOff is returned by the admin AI reads when no worker is attached, so the
// TUI can show an explanatory message instead of an error.
var ErrAIOff = errors.New("BETanIA is not enabled")

// ErrAIBusy is returned by TriggerAI when a pass is already queued, so a double
// press is reported as a no-op rather than an error.
var ErrAIBusy = errors.New("a BETanIA run is already queued")

// SetAIMonitor attaches the live worker's monitor. Optional — when unset, the
// admin BETanIA panel reports that it's disabled.
func (s *Service) SetAIMonitor(m AIMonitor) { s.ai = m }

// SetAITrigger attaches the live worker's "run now" hook (ai.Bettor.Trigger).
// Optional — when unset, TriggerAI reports the worker is off. The func returns
// false when a pass is already queued.
func (s *Service) SetAITrigger(fn func() bool) { s.aiTrigger = fn }

// TriggerAI asks the live worker to run a pass immediately. Admin only. Returns
// ErrAIOff when no worker is attached and ErrAIBusy when one is already queued.
func (s *Service) TriggerAI(by *models.User) error {
	if err := requireAdmin(by); err != nil {
		return err
	}
	if s.aiTrigger == nil {
		return ErrAIOff
	}
	if !s.aiTrigger() {
		return ErrAIBusy
	}
	return nil
}

// AIStatus returns BETanIA's live-worker status. Admin only.
func (s *Service) AIStatus(by *models.User) (ai.Status, error) {
	if err := requireAdmin(by); err != nil {
		return ai.Status{}, err
	}
	if s.ai == nil {
		return ai.Status{}, ErrAIOff
	}
	return s.ai.Status(), nil
}

// AIActivity returns BETanIA's most recent live decisions, newest first. Admin only.
func (s *Service) AIActivity(by *models.User, limit int) ([]ai.Action, error) {
	if err := requireAdmin(by); err != nil {
		return nil, err
	}
	if s.ai == nil {
		return nil, ErrAIOff
	}
	return s.ai.Activity(limit), nil
}

// AIBet is one of BETanIA's persisted predictions with match context and (for
// finished matches) the points it earned. Unlike ai.Action — the volatile
// in-memory feed the live worker fills, which a restart wipes — this is sourced
// from the DB, so it always reflects every pick BETanIA has on record.
type AIBet struct {
	Match  models.Match
	Bet    models.Bet
	Points int // only meaningful when Match.Finished
}

// AIBets returns BETanIA's picks on record, most-recently-placed first, with
// live scores overlaid and points scored for finished matches. Admin only.
// Returns ErrAIOff when BETanIA has never been onboarded (no user / no bets).
func (s *Service) AIBets(by *models.User, limit int) ([]AIBet, error) {
	if err := requireAdmin(by); err != nil {
		return nil, err
	}
	bot, err := s.store.UserByFingerprint(ai.Fingerprint)
	if errors.Is(err, db.ErrNotFound) {
		return nil, ErrAIOff
	}
	if err != nil {
		return nil, err
	}
	bets, err := s.store.BetsForUser(bot.ID, s.tournamentID)
	if err != nil {
		return nil, err
	}
	matches, err := s.store.ListMatches(s.tournamentID)
	if err != nil {
		return nil, err
	}
	snap := s.liveSnapshot()
	byID := make(map[int64]models.Match, len(matches))
	for i := range matches {
		overlayLive(&matches[i], snap)
		byID[matches[i].ID] = matches[i]
	}
	sc, err := s.newScorer()
	if err != nil {
		return nil, err
	}
	out := make([]AIBet, 0, len(bets))
	for _, b := range bets {
		m, ok := byID[b.MatchID]
		if !ok {
			continue
		}
		ab := AIBet{Match: m, Bet: b}
		if m.Finished {
			ab.Points = sc.points(b, m)
		}
		out = append(out, ab)
	}
	// Most-recently-decided first; ties (e.g. the bulk seed) break by kickoff,
	// soonest-kicking-off first so upcoming picks lead within a batch.
	sort.Slice(out, func(i, j int) bool {
		ti, tj := out[i].Bet.UpdatedAt, out[j].Bet.UpdatedAt
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return out[i].Match.StartsAt.Before(out[j].Match.StartsAt)
	})
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}
