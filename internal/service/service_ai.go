package service

import (
	"errors"

	"bethoven/internal/ai"
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
