// Package results pulls finished match results from an external feed and feeds
// them into the service so the leaderboard updates without an admin typing
// scores. It holds the I/O-free types and the polling loop; concrete providers
// (e.g. football-data.org) live in subpackages and implement Fetcher.
//
// The layering mirrors the rest of BEThoven: this package and its providers do
// the I/O, while every betting/scoring rule stays in internal/service. The
// poller is deliberately thin — fetch, hand the data to the service, log.
package results

import (
	"context"
	"log"
	"time"
)

// Score is a regulation 90-minute scoreline, oriented as the FEED reports it
// (A = home team, B = away team). The service re-orients it to each match's
// stored team order before recording the result.
type Score struct {
	A int
	B int
}

// FeedMatch is one fixture as seen in the external feed, reduced to what we
// need to reconcile and score it.
type FeedMatch struct {
	ExternalRef string    // stable feed match id
	HomeTeam    string    // feed's name for the home team
	AwayTeam    string    // feed's name for the away team
	KickoffUTC  time.Time // scheduled kickoff (UTC), used as a reconciliation guard
	Finished    bool      // true once the match has a final result
	// Reg90 is the regulation 90-minute score (ET/penalties excluded), or nil
	// when the match isn't final or the feed doesn't let us derive the 90' score
	// for a knockout that went to extra time. A nil Reg90 is never auto-applied.
	Reg90 *Score
}

// Report summarises what one sync pass did, for logging.
type Report struct {
	Applied   int      // results written this pass
	Skipped   int      // finished feed matches we deliberately didn't write
	Unmatched []string // feed matches we couldn't map to a local fixture
}

// Fetcher retrieves the current set of feed matches. Implemented by providers.
type Fetcher interface {
	Fetch(ctx context.Context) ([]FeedMatch, error)
}

// Applier reconciles feed matches against local fixtures and records results.
// *service.Service satisfies this; the interface keeps this package free of a
// dependency on service (avoiding an import cycle).
type Applier interface {
	ApplyFeedResults([]FeedMatch) (Report, error)
}

// RunPoller fetches results on a fixed interval and applies them, until ctx is
// cancelled. It runs one pass immediately so a freshly-started server catches up
// without waiting a full interval. Errors are logged, never fatal: a flaky feed
// must never take the SSH server down.
func RunPoller(ctx context.Context, f Fetcher, a Applier, interval time.Duration) {
	syncOnce(ctx, f, a)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("results: poller stopped")
			return
		case <-ticker.C:
			syncOnce(ctx, f, a)
		}
	}
}

// syncOnce performs a single fetch+apply pass and logs the outcome.
func syncOnce(ctx context.Context, f Fetcher, a Applier) {
	matches, err := f.Fetch(ctx)
	if err != nil {
		log.Printf("results: fetch failed: %v", err)
		return
	}
	rep, err := a.ApplyFeedResults(matches)
	if err != nil {
		log.Printf("results: apply failed: %v", err)
		return
	}
	if rep.Applied > 0 || len(rep.Unmatched) > 0 {
		log.Printf("results: applied=%d skipped=%d unmatched=%v",
			rep.Applied, rep.Skipped, rep.Unmatched)
	}
}
