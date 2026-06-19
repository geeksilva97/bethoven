package service

import (
	"errors"
	"testing"

	"bethoven/internal/ai"
)

// fakeUsageSource is a stand-in for ai.UsageLog: the service AIUsage read just
// gates and delegates, so a canned report is enough to exercise it.
type fakeUsageSource struct {
	rep ai.UsageReport
	err error
}

func (f fakeUsageSource) Report() (ai.UsageReport, error) { return f.rep, f.err }

func TestAIUsage(t *testing.T) {
	svc, _, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")
	player, _ := svc.Register("SHA256:nosy", testInvite, "Nosy")

	// No source attached -> ErrAIOff (BETanIA not running).
	if _, err := svc.AIUsage(admin); !errors.Is(err, ErrAIOff) {
		t.Fatalf("AIUsage with no source: want ErrAIOff, got %v", err)
	}

	want := ai.UsageReport{Total: ai.CategoryUsage{Calls: 5, InputTokens: 1000, EstCostUSD: 1.23}}
	svc.SetAIUsageSource(fakeUsageSource{rep: want})

	// Player is rejected even when a source is attached (admin-only).
	if _, err := svc.AIUsage(player); !errors.Is(err, ErrForbidden) {
		t.Fatalf("AIUsage by player: want ErrForbidden, got %v", err)
	}

	// Admin gets the report through unchanged.
	got, err := svc.AIUsage(admin)
	if err != nil {
		t.Fatalf("AIUsage by admin: %v", err)
	}
	if got.Total.Calls != 5 || got.Total.InputTokens != 1000 || got.Total.EstCostUSD != 1.23 {
		t.Errorf("AIUsage report = %+v, want %+v", got.Total, want.Total)
	}
}
