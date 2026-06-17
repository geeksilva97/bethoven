package analytics

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// bufferSize bounds the in-memory event queue. At our traffic (one small SSH
// server) this is never approached; if it ever filled, dropping is correct —
// analytics must never apply backpressure to the betting path.
const bufferSize = 1024

// Recorder is the async writer behind the service's AnalyticsSink port. Track
// hands an event to a background goroutine and returns immediately; the
// goroutine persists events one at a time via the Store. It also exposes the
// read queries the admin panel needs, delegating straight to the Store.
type Recorder struct {
	store *Store
	ch    chan Event
	stop  chan struct{}
	done  chan struct{}
	once  sync.Once

	dropped atomic.Int64
}

// NewRecorder starts the background writer. Call Close on shutdown to drain.
func NewRecorder(store *Store) *Recorder {
	r := &Recorder{
		store: store,
		ch:    make(chan Event, bufferSize),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go r.run()
	return r
}

func (r *Recorder) run() {
	defer close(r.done)
	for {
		select {
		case ev := <-r.ch:
			r.write(ev)
		case <-r.stop:
			// Drain whatever is buffered, then exit.
			for {
				select {
				case ev := <-r.ch:
					r.write(ev)
				default:
					return
				}
			}
		}
	}
}

func (r *Recorder) write(ev Event) {
	if err := r.store.Insert(ev); err != nil {
		log.Printf("analytics: insert %q: %v", ev.Name, err)
	}
}

// Track enqueues an event without blocking. If the buffer is full the event is
// dropped (and counted) rather than stalling the caller — the whole point is
// that recording can never slow down or fail a bet. Never returns an error.
//
// The channel is never closed (Close signals via a separate channel), so Track
// is safe to call concurrently with, and even after, Close.
func (r *Recorder) Track(ev Event) {
	select {
	case r.ch <- ev:
	default:
		if n := r.dropped.Add(1); n == 1 || n%100 == 0 {
			log.Printf("analytics: buffer full, dropped %d event(s)", n)
		}
	}
}

// Dropped reports how many events have been discarded due to a full buffer.
func (r *Recorder) Dropped() int64 { return r.dropped.Load() }

// Close stops accepting work, drains the buffered events, and waits for the
// writer to finish (or ctx to cancel). Idempotent.
func (r *Recorder) Close(ctx context.Context) error {
	r.once.Do(func() { close(r.stop) })
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// --- AnalyticsSink read side: delegate to the store -----------------------

func (r *Recorder) Overview(now time.Time) (Overview, error) { return r.store.Overview(now) }
func (r *Recorder) Recent(limit int) ([]Event, error)        { return r.store.Recent(limit) }
func (r *Recorder) PerPlayer() ([]PlayerStat, error)         { return r.store.PerPlayer() }
func (r *Recorder) Timeline(now time.Time, days int) ([]Bucket, error) {
	return r.store.Timeline(now, days)
}
