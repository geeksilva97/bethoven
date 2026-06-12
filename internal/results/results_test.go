package results

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// fakeFetcher returns canned matches (or an error) and counts its calls. calls
// is atomic because the poller invokes Fetch from its own goroutine.
type fakeFetcher struct {
	matches []FeedMatch
	err     error
	calls   atomic.Int64
}

func (f *fakeFetcher) Fetch(context.Context) ([]FeedMatch, error) {
	f.calls.Add(1)
	return f.matches, f.err
}

// recordingApplier captures the batches it was handed.
type recordingApplier struct {
	batches [][]FeedMatch
	rep     Report
	err     error
}

func (a *recordingApplier) ApplyFeedResults(feed []FeedMatch) (Report, error) {
	a.batches = append(a.batches, feed)
	return a.rep, a.err
}

func TestSyncOncePassesFeedToApplier(t *testing.T) {
	f := &fakeFetcher{matches: []FeedMatch{{ExternalRef: "1", Finished: true, Reg90: &Score{A: 1, B: 0}}}}
	a := &recordingApplier{rep: Report{Applied: 1}}

	syncOnce(context.Background(), f, a)

	if f.calls.Load() != 1 {
		t.Errorf("fetch calls = %d, want 1", f.calls.Load())
	}
	if len(a.batches) != 1 || len(a.batches[0]) != 1 || a.batches[0][0].ExternalRef != "1" {
		t.Errorf("applier did not receive the fetched feed: %+v", a.batches)
	}
}

func TestSyncOnceSkipsApplyOnFetchError(t *testing.T) {
	f := &fakeFetcher{err: errors.New("network down")}
	a := &recordingApplier{}

	syncOnce(context.Background(), f, a)

	if len(a.batches) != 0 {
		t.Errorf("apply must not run when fetch fails, got %d batches", len(a.batches))
	}
}

// panicFetcher panics instead of returning — simulating an unforeseen crash in
// the feed path (the kind an unofficial endpoint could provoke).
type panicFetcher struct{}

func (panicFetcher) Fetch(context.Context) ([]FeedMatch, error) {
	panic("boom from the feed")
}

// TestSyncOnceRecoversFromPanic: a panic in the feed path must be contained, not
// propagated — otherwise it would crash the whole SSH server. syncOnce returning
// normally here proves the recover guard holds.
func TestSyncOnceRecoversFromPanic(t *testing.T) {
	a := &recordingApplier{}

	syncOnce(context.Background(), panicFetcher{}, a) // must not panic

	if len(a.batches) != 0 {
		t.Errorf("apply must not run after a fetch panic, got %d batches", len(a.batches))
	}
}

// TestRunPollerRunsImmediatelyThenStops verifies the poller does one pass up
// front (not after a full interval) and exits when its context is cancelled.
func TestRunPollerRunsImmediatelyThenStops(t *testing.T) {
	f := &fakeFetcher{}
	a := &recordingApplier{}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		RunPoller(ctx, f, a, time.Hour) // long interval: only the immediate pass should run
		close(done)
	}()

	// Wait for the immediate pass to land.
	deadline := time.After(2 * time.Second)
	for f.calls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("poller did not run an immediate pass")
		case <-time.After(time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("poller did not stop on context cancel")
	}
	if f.calls.Load() != 1 {
		t.Errorf("expected exactly 1 pass with a 1h interval, got %d", f.calls.Load())
	}
}
