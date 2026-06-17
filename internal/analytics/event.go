package analytics

import "time"

// Event is one recorded action. Props holds event-specific fields (marshaled to
// JSON on write). Actor is the display name at event time, denormalized so the
// admin panel never has to join against the domain database.
type Event struct {
	At          time.Time
	UserID      int64 // 0 when unregistered/anonymous
	Fingerprint string
	Actor       string
	Name        string
	Props       map[string]string
}

// Event name constants for the rows the analytics queries care about. These MUST
// stay in sync with the emit-site constants in package service (which owns the
// authoritative list). Kept here only so the query layer reads clearly.
const (
	nameSession   = "session_start"
	nameBetPlaced = "bet_placed"
)

// Overview is the headline KPI block for the admin panel. RegisteredPlayers is
// the only field not derivable from the event log alone; the service fills it
// from the domain user table (so it counts players who registered before
// analytics was enabled).
type Overview struct {
	TotalAccesses     int
	UniquePlayers     int
	AccessesToday     int
	Accesses7d        int
	BetsPlaced        int
	ActivePlayers     int // distinct players with any event in the last 7 days
	RegisteredPlayers int // filled by the service from the domain store
}

// PlayerStat is one row of the per-player engagement table. Actor carries the
// most recent name seen in the log; the service overrides it with the player's
// current display name when the user id is known.
type PlayerStat struct {
	UserID   int64
	Actor    string
	Accesses int
	Bets     int
	LastSeen time.Time
}

// Bucket is one day of the activity-over-time histogram (Count = accesses).
type Bucket struct {
	Day   string // YYYY-MM-DD (UTC)
	Count int
}
