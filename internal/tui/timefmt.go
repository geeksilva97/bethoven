package tui

import "time"

// displayLoc is the timezone every kickoff/result time is rendered in. Storage
// and the kickoff lock stay in UTC; only the display (and admin time input) use
// this zone. Defaults to UTC and is overridden once at startup via SetLocation
// from BETHOVEN_TIMEZONE. Set-once-before-serving, so it's concurrency-safe.
var displayLoc = time.UTC

// SetLocation sets the TUI display timezone. A nil loc is ignored (keeps UTC).
func SetLocation(loc *time.Location) {
	if loc != nil {
		displayLoc = loc
	}
}

// fmtKickoff renders a stored (UTC) time in the display zone, with the zone's
// abbreviation/offset so it's unambiguous — e.g. "Thu 18 Jun 16:00 -03".
func fmtKickoff(t time.Time) string {
	return t.In(displayLoc).Format("Mon 02 Jan 15:04 MST")
}
