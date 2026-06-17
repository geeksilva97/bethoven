package analytics

import (
	"context"
	"testing"
	"time"
)

var t0 = time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	conn, err := Open(t.TempDir() + "/analytics.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return NewStore(conn)
}

// TestRecorderPersistsAndDrains verifies the async writer eventually persists
// every enqueued event, and Close drains what's buffered.
func TestRecorderPersistsAndDrains(t *testing.T) {
	st := newTestStore(t)
	r := NewRecorder(st)
	for i := 0; i < 5; i++ {
		r.Track(Event{At: t0, UserID: 1, Actor: "Alice", Name: nameSession})
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	got, err := st.Recent(100)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(got) != 5 {
		t.Errorf("want 5 persisted events, got %d", len(got))
	}
}

// TestTrackNeverBlocksAndAccountsForEveryEvent floods the recorder far past its
// buffer. Track must return promptly for every call (the test would hang
// otherwise), and every event must be either persisted or counted as dropped —
// never silently lost. This is the guarantee that analytics can't stall a bet.
func TestTrackNeverBlocksAndAccountsForEveryEvent(t *testing.T) {
	st := newTestStore(t)
	r := NewRecorder(st)
	const n = 5000
	for i := 0; i < n; i++ {
		r.Track(Event{At: t0, Name: nameSession})
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	got, err := st.Recent(n + 1)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if persisted := int64(len(got)); persisted+r.Dropped() != n {
		t.Errorf("persisted(%d) + dropped(%d) = %d, want %d", persisted, r.Dropped(), persisted+r.Dropped(), n)
	}
}

// TestCloseIsIdempotent ensures a double Close doesn't panic.
func TestCloseIsIdempotent(t *testing.T) {
	r := NewRecorder(newTestStore(t))
	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("second close: %v", err)
	}
	// Track after Close must not panic (channel is never closed).
	r.Track(Event{At: t0, Name: nameSession})
}

// TestInsertRoundTripsProps checks the JSON props column survives a round trip.
func TestInsertRoundTripsProps(t *testing.T) {
	st := newTestStore(t)
	in := Event{At: t0, UserID: 7, Actor: "Bob", Name: nameBetPlaced, Props: map[string]string{"pred": "2-1", "match": "BRA-CRO"}}
	if err := st.Insert(in); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := st.Recent(1)
	if err != nil || len(got) != 1 {
		t.Fatalf("recent: %v (%d rows)", err, len(got))
	}
	ev := got[0]
	if ev.UserID != 7 || ev.Actor != "Bob" || ev.Name != nameBetPlaced {
		t.Errorf("scalar fields wrong: %+v", ev)
	}
	if ev.Props["pred"] != "2-1" || ev.Props["match"] != "BRA-CRO" {
		t.Errorf("props not round-tripped: %v", ev.Props)
	}
}

// seed inserts a fixed dataset for the query tests, relative to t0 (a 12:00 UTC
// "today").
func seed(t *testing.T, st *Store) {
	t.Helper()
	must := func(e Event) {
		if err := st.Insert(e); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	must(Event{At: t0, UserID: 1, Actor: "Alice", Name: nameSession})                  // today
	must(Event{At: t0.Add(-time.Hour), UserID: 1, Actor: "Alice", Name: nameSession})  // today
	must(Event{At: t0.AddDate(0, 0, -3), UserID: 2, Actor: "Bob", Name: nameSession})  // 3 days ago
	must(Event{At: t0.AddDate(0, 0, -30), UserID: 3, Actor: "Old", Name: nameSession}) // 30 days ago
	must(Event{At: t0, UserID: 1, Actor: "Alice", Name: nameBetPlaced})                // a bet
}

func TestOverview(t *testing.T) {
	st := newTestStore(t)
	seed(t, st)
	ov, err := st.Overview(t0)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if ov.TotalAccesses != 4 {
		t.Errorf("TotalAccesses = %d, want 4", ov.TotalAccesses)
	}
	if ov.UniquePlayers != 3 {
		t.Errorf("UniquePlayers = %d, want 3", ov.UniquePlayers)
	}
	if ov.AccessesToday != 2 {
		t.Errorf("AccessesToday = %d, want 2", ov.AccessesToday)
	}
	if ov.Accesses7d != 3 {
		t.Errorf("Accesses7d = %d, want 3", ov.Accesses7d)
	}
	if ov.BetsPlaced != 1 {
		t.Errorf("BetsPlaced = %d, want 1", ov.BetsPlaced)
	}
	if ov.ActivePlayers != 2 { // users 1 and 2 within 7 days; user 3 is 30 days old
		t.Errorf("ActivePlayers = %d, want 2", ov.ActivePlayers)
	}
}

func TestPerPlayer(t *testing.T) {
	st := newTestStore(t)
	seed(t, st)
	rows, err := st.PerPlayer()
	if err != nil {
		t.Fatalf("per-player: %v", err)
	}
	byUser := map[int64]PlayerStat{}
	for _, r := range rows {
		byUser[r.UserID] = r
	}
	if got := byUser[1]; got.Accesses != 2 || got.Bets != 1 {
		t.Errorf("user 1 = %+v, want accesses 2 bets 1", got)
	}
	if got := byUser[2]; got.Accesses != 1 || got.Bets != 0 {
		t.Errorf("user 2 = %+v, want accesses 1 bets 0", got)
	}
}

func TestTimeline(t *testing.T) {
	st := newTestStore(t)
	seed(t, st)
	// Last 7 days: today (2 sessions) and 3 days ago (1). The 30-day-old one is
	// outside the window.
	buckets, err := st.Timeline(t0, 7)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if len(buckets) != 2 {
		t.Fatalf("want 2 day-buckets, got %d (%+v)", len(buckets), buckets)
	}
	total := 0
	for _, b := range buckets {
		total += b.Count
	}
	if total != 3 {
		t.Errorf("timeline total = %d, want 3", total)
	}
}
