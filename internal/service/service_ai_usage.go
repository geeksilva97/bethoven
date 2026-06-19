package service

import (
	"bethoven/internal/ai"
	"bethoven/internal/models"
)

// AIUsageSource is the optional read port for BETanIA's cumulative Claude token
// usage. The concrete implementation (ai.UsageLog) aggregates the durable on-disk
// usage log; nil is valid and means "no usage recorded" — the read returns
// ErrAIOff. Mirrors the AIMonitor / AICommentMonitor ports, except it's backed by
// a persistent log so totals survive restarts (the monitor rings don't).
type AIUsageSource interface {
	Report() (ai.UsageReport, error)
}

// SetAIUsageSource attaches the usage-log reader. Optional — when unset, the admin
// Usage tab reports that BETanIA isn't running.
func (s *Service) SetAIUsageSource(src AIUsageSource) { s.aiUsage = src }

// AIUsage returns BETanIA's cumulative token usage and estimated cost, broken down
// by category (bets / comments / live commentary). Admin only. The figures are
// read from the on-disk usage log, so they persist across restarts and deploys.
func (s *Service) AIUsage(by *models.User) (ai.UsageReport, error) {
	if err := requireAdmin(by); err != nil {
		return ai.UsageReport{}, err
	}
	if s.aiUsage == nil {
		return ai.UsageReport{}, ErrAIOff
	}
	return s.aiUsage.Report()
}
