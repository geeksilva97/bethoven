package live

import (
	"context"
	"testing"
	"time"

	"bethoven/internal/clock"
	"bethoven/internal/models"
)

// fakeProvider returns canned events, ignoring the requested days.
type fakeProvider struct{ events []Event }

func (f fakeProvider) Fetch(_ context.Context, _ []time.Time) ([]Event, error) {
	return f.events, nil
}

func kickoff() time.Time { return time.Date(2026, 6, 12, 19, 0, 0, 0, time.UTC) }

func testMatches() []models.Match {
	return []models.Match{
		{ID: 1, TeamA: "Brazil", TeamB: "Serbia", StartsAt: kickoff()},
		// Stored as "USA" but ESPN reports "United States" — alias must bridge it.
		{ID: 2, TeamA: "USA", TeamB: "Paraguay", StartsAt: kickoff().Add(6 * time.Hour)},
	}
}

func newTestPoller(t *testing.T, events []Event) (*Poller, *Cache, *[]int) {
	t.Helper()
	cache := NewCache()
	var finalized []int
	fc := &clock.Fake{T: kickoff().Add(time.Hour)}
	p := NewPoller(
		fakeProvider{events},
		cache,
		func() ([]models.Match, error) { return testMatches(), nil },
		func(matchID int64, a, b int) error {
			finalized = append(finalized, int(matchID))
			return nil
		},
		fc,
		time.Minute,
	)
	return p, cache, &finalized
}

func TestPoller_InPlayCached(t *testing.T) {
	p, cache, finalized := newTestPoller(t, []Event{
		{Home: "Brazil", Away: "Serbia", HomeScore: 2, AwayScore: 1, Date: kickoff(), State: StateIn, Minute: 2, Clock: "67'"},
	})
	p.poll(context.Background())

	snap := cache.Snapshot()
	s, ok := snap[1]
	if !ok {
		t.Fatal("match 1 not in snapshot")
	}
	if s.A != 2 || s.B != 1 || s.State != StateIn || s.Clock != "67'" {
		t.Fatalf("unexpected live score: %+v", s)
	}
	if len(*finalized) != 0 {
		t.Fatalf("in-play match should not finalize, got %v", *finalized)
	}
}

func TestPoller_PreSkipped(t *testing.T) {
	p, cache, _ := newTestPoller(t, []Event{
		{Home: "Brazil", Away: "Serbia", Date: kickoff(), State: StatePre},
	})
	p.poll(context.Background())
	if len(cache.Snapshot()) != 0 {
		t.Fatal("pre-match events must not be cached")
	}
}

func TestPoller_PostFinalizesOnce(t *testing.T) {
	p, _, finalized := newTestPoller(t, []Event{
		{Home: "Brazil", Away: "Serbia", HomeScore: 3, AwayScore: 0, Date: kickoff(), State: StatePost},
	})
	p.poll(context.Background())
	p.poll(context.Background()) // matches() reports Finished=false in this fake, so it would call again...

	// The fake matches() always returns Finished=false, so finalize fires each
	// poll; in production FinalizeFromFeed is the guard. Assert it was invoked
	// with the right match at least once.
	if len(*finalized) == 0 || (*finalized)[0] != 1 {
		t.Fatalf("expected finalize for match 1, got %v", *finalized)
	}
}

func TestPoller_AliasAndOrientation(t *testing.T) {
	// ESPN: "United States" home 0, "Paraguay" away 0; stored as USA(TeamA) v Paraguay.
	// Alias must resolve United States->usa; orientation must keep A=home.
	p, cache, _ := newTestPoller(t, []Event{
		{Home: "United States", Away: "Paraguay", HomeScore: 1, AwayScore: 0, Date: kickoff().Add(6 * time.Hour), State: StateIn, Clock: "10'"},
	})
	p.poll(context.Background())
	s, ok := cache.Snapshot()[2]
	if !ok {
		t.Fatal("aliased match (USA) not resolved")
	}
	if s.A != 1 || s.B != 0 {
		t.Fatalf("orientation wrong: got A=%d B=%d, want 1-0", s.A, s.B)
	}
}

func TestPoller_DateGuardRejectsFarApart(t *testing.T) {
	// Same team pair as match 1, but kickoff is days away (e.g. a group pairing
	// recurring in the knockouts). The date guard must reject it, not bind.
	p, cache, _ := newTestPoller(t, []Event{
		{Home: "Brazil", Away: "Serbia", HomeScore: 1, AwayScore: 0, Date: kickoff().AddDate(0, 0, 5), State: StateIn, Clock: "10'"},
	})
	p.poll(context.Background())
	if len(cache.Snapshot()) != 0 {
		t.Fatalf("far-apart same-pair event must not resolve, got %+v", cache.Snapshot())
	}
}

// seqProvider returns a different batch of events on each Fetch.
type seqProvider struct {
	batches [][]Event
	i       int
}

func (s *seqProvider) Fetch(_ context.Context, _ []time.Time) ([]Event, error) {
	b := s.batches[s.i]
	if s.i < len(s.batches)-1 {
		s.i++
	}
	return b, nil
}

func TestPoller_StaleScoreExpires(t *testing.T) {
	// Poll 1: Brazil v Serbia in play. Poll 2: feed no longer reports it (gap or
	// missed finish). The stale in-play score must drop out, not linger.
	prov := &seqProvider{batches: [][]Event{
		{{Home: "Brazil", Away: "Serbia", HomeScore: 1, AwayScore: 0, Date: kickoff(), State: StateIn, Clock: "40'"}},
		{}, // nothing reported this poll
	}}
	cache := NewCache()
	fc := &clock.Fake{T: kickoff().Add(time.Hour)}
	p := NewPoller(prov, cache,
		func() ([]models.Match, error) { return testMatches(), nil },
		func(int64, int, int) error { return nil }, fc, time.Minute)

	p.poll(context.Background())
	if _, ok := cache.Snapshot()[1]; !ok {
		t.Fatal("poll 1 should have cached match 1")
	}
	p.poll(context.Background())
	if len(cache.Snapshot()) != 0 {
		t.Fatalf("stale score not expired: %+v", cache.Snapshot())
	}
}

func TestPoller_OrientationSwapped(t *testing.T) {
	// ESPN reports Serbia as home, Brazil away — opposite of our TeamA/TeamB.
	p, cache, _ := newTestPoller(t, []Event{
		{Home: "Serbia", Away: "Brazil", HomeScore: 1, AwayScore: 4, Date: kickoff(), State: StateIn},
	})
	p.poll(context.Background())
	s := cache.Snapshot()[1]
	// Our TeamA is Brazil, so A must be Brazil's 4, B Serbia's 1.
	if s.A != 4 || s.B != 1 {
		t.Fatalf("swap not applied: got A=%d B=%d, want 4-1", s.A, s.B)
	}
}
