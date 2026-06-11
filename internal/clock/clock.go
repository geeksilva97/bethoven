// Package clock provides an injectable time source. Production uses the real
// wall clock; tests use Fake to make time-dependent rules (the kickoff lock)
// deterministic.
package clock

import "time"

// Clock is the minimal time interface the service depends on.
type Clock interface {
	// Now returns the current time. Callers normalise to UTC themselves.
	Now() time.Time
}

// Real is the production clock backed by time.Now.
type Real struct{}

func (Real) Now() time.Time { return time.Now() }

// Fake is a controllable clock for tests. Set T to the desired instant and
// advance it with Add.
type Fake struct{ T time.Time }

func (f *Fake) Now() time.Time { return f.T }

// Add moves the fake clock forward (or backward, with a negative duration).
func (f *Fake) Add(d time.Duration) { f.T = f.T.Add(d) }
