package service

import (
	"errors"
	"sync"
	"testing"
	"time"

	"bethoven/internal/analytics"
	"bethoven/internal/models"
	"bethoven/internal/scoring"
)

// fakeSink is an in-memory AnalyticsSink that records what the service emits, so
// tests can assert the emit path without a real database or goroutine.
type fakeSink struct {
	mu     sync.Mutex
	events []analytics.Event
}

func (f *fakeSink) Track(ev analytics.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
}

func (f *fakeSink) byName(name string) []analytics.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []analytics.Event
	for _, e := range f.events {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
}

func (f *fakeSink) Overview(time.Time) (analytics.Overview, error)      { return analytics.Overview{}, nil }
func (f *fakeSink) Recent(int) ([]analytics.Event, error)               { return nil, nil }
func (f *fakeSink) PerPlayer() ([]analytics.PlayerStat, error)          { return nil, nil }
func (f *fakeSink) Timeline(time.Time, int) ([]analytics.Bucket, error) { return nil, nil }

// futureMatch adds a match that kicks off after the fake clock's "now" so a bet
// is allowed, returning its id.
func futureMatch(t *testing.T, svc *Service, admin *models.User) int64 {
	t.Helper()
	id, err := svc.AddMatch(admin, "BRA", "CRO", models.PhaseGroup, "Group A", base.Add(time.Hour))
	if err != nil {
		t.Fatalf("AddMatch: %v", err)
	}
	return id
}

func TestEmitsRegisteredAndBetPlaced(t *testing.T) {
	svc, _, _ := newTestService(t)
	sink := &fakeSink{}
	svc.SetAnalyticsSink(sink)

	admin, err := svc.Register(adminFP, "", "Boss")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := svc.Register("SHA256:alice", testInvite, "Alice")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	matchID := futureMatch(t, svc, admin)
	if err := svc.PlaceBet(alice.ID, matchID, 2, 1); err != nil {
		t.Fatalf("PlaceBet: %v", err)
	}

	// registered: one per account, carrying the display name.
	reg := sink.byName(EvRegistered)
	if len(reg) != 2 {
		t.Fatalf("want 2 registered events, got %d", len(reg))
	}

	// bet_placed: carries the bettor's id and the right props, with NO actor
	// resolved on the emit path (that happens later, at read time).
	bets := sink.byName(EvBetPlaced)
	if len(bets) != 1 {
		t.Fatalf("want 1 bet_placed event, got %d", len(bets))
	}
	ev := bets[0]
	if ev.UserID != alice.ID {
		t.Errorf("bet_placed UserID = %d, want %d", ev.UserID, alice.ID)
	}
	if ev.Props["match"] != "BRA-CRO" || ev.Props["pred"] != "2-1" {
		t.Errorf("bet_placed props = %v", ev.Props)
	}
	if ev.Actor != "" {
		t.Errorf("bet_placed should not resolve an actor on the emit path, got %q", ev.Actor)
	}
}

func TestEmitsAdminActions(t *testing.T) {
	svc, _, _ := newTestService(t)
	sink := &fakeSink{}
	svc.SetAnalyticsSink(sink)

	admin, _ := svc.Register(adminFP, "", "Boss")
	matchID := futureMatch(t, svc, admin)

	if err := svc.SetScoringMode(admin, scoring.ModeProximity); err != nil {
		t.Fatalf("SetScoringMode: %v", err)
	}
	if err := svc.EnterResult(admin, matchID, 3, 0); err != nil {
		t.Fatalf("EnterResult: %v", err)
	}

	if got := len(sink.byName(EvMatchAdded)); got != 1 {
		t.Errorf("want 1 match_added, got %d", got)
	}
	if got := len(sink.byName(EvSettingChanged)); got != 1 {
		t.Errorf("want 1 setting_changed, got %d", got)
	}
	if got := len(sink.byName(EvResultEntered)); got != 1 {
		t.Errorf("want 1 result_entered, got %d", got)
	}
}

// TestNilSinkIsNoOp confirms the domain path is unaffected when analytics is off:
// no sink attached, everything still works, nothing panics.
func TestNilSinkIsNoOp(t *testing.T) {
	svc, _, _ := newTestService(t)
	// deliberately no SetAnalyticsSink

	admin, _ := svc.Register(adminFP, "", "Boss")
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	matchID := futureMatch(t, svc, admin)
	if err := svc.PlaceBet(alice.ID, matchID, 1, 0); err != nil {
		t.Fatalf("PlaceBet with nil sink failed: %v", err)
	}
}

func TestAnalyticsQueriesRequireAdmin(t *testing.T) {
	svc, _, _ := newTestService(t)
	svc.SetAnalyticsSink(&fakeSink{})
	player, _ := svc.Register("SHA256:alice", testInvite, "Alice")

	if _, err := svc.AnalyticsOverview(player); !errors.Is(err, ErrForbidden) {
		t.Errorf("Overview by player: want ErrForbidden, got %v", err)
	}
	if _, err := svc.AnalyticsRecent(player, 10); !errors.Is(err, ErrForbidden) {
		t.Errorf("Recent by player: want ErrForbidden, got %v", err)
	}
	if _, err := svc.AnalyticsPerPlayer(player); !errors.Is(err, ErrForbidden) {
		t.Errorf("PerPlayer by player: want ErrForbidden, got %v", err)
	}
	if _, err := svc.AnalyticsTimeline(player, 7); !errors.Is(err, ErrForbidden) {
		t.Errorf("Timeline by player: want ErrForbidden, got %v", err)
	}
}

func TestAnalyticsOffReturnsSentinel(t *testing.T) {
	svc, _, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")
	// No sink attached: an admin query must report disabled, not error out.
	if _, err := svc.AnalyticsOverview(admin); !errors.Is(err, ErrAnalyticsOff) {
		t.Errorf("want ErrAnalyticsOff, got %v", err)
	}
}
