// Package models holds BEThoven's pure domain types, shared by the store,
// scoring, and service layers. They carry no behaviour and no DB dependency,
// which keeps scoring a testable leaf package.
package models

import "time"

// Role is a user's permission level.
type Role string

const (
	RolePlayer Role = "player"
	RoleAdmin  Role = "admin"
)

// Phase is a stage of the tournament. Group fixtures are known up front;
// knockout matches are added by the admin as matchups are decided.
type Phase string

const (
	PhaseGroup   Phase = "group"
	PhaseRound16 Phase = "round_16"
	PhaseRound8  Phase = "round_8"
	PhaseSemi    Phase = "semi"
	PhaseFinal   Phase = "final"
)

// Tournament scopes a set of matches and bets (e.g. "World Cup 2026").
type Tournament struct {
	ID        int64
	Name      string
	Active    bool
	CreatedAt time.Time
}

// User is identified by the SHA256 fingerprint of their SSH public key.
type User struct {
	ID          int64
	Fingerprint string
	DisplayName string
	Role        Role
	CreatedAt   time.Time
}

// Match is a single fixture. ScoreA/ScoreB are nil until a result is recorded
// (by the admin or the results poller); for knockouts they hold the regulation
// 90-minute score.
type Match struct {
	ID           int64
	TournamentID int64
	TeamA        string
	TeamB        string
	Phase        Phase
	GroupLabel   string // e.g. "Group G"; empty for knockouts
	StartsAt     time.Time
	ScoreA       *int
	ScoreB       *int
	Finished     bool
	// ExternalRef links this match to its row in the external results feed
	// (ESPN event id). Empty until the poller reconciles it. Lets us re-find the
	// same match across polls without re-matching on team names.
	ExternalRef string
}

// Bet is a user's prediction for a match: a scoreline. One editable bet per
// (user, match) until kickoff.
type Bet struct {
	ID        int64
	UserID    int64
	MatchID   int64
	PredA     int
	PredB     int
	CreatedAt time.Time
	UpdatedAt time.Time
}
