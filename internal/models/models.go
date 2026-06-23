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

// Label is the human-facing name of a tournament phase, used wherever a phase is
// shown to players (the scoring-rules ladder, the round-weight breakdown line).
func (p Phase) Label() string {
	switch p {
	case PhaseGroup:
		return "Group stage"
	case PhaseRound16:
		return "Round of 16"
	case PhaseRound8:
		return "Quarter-final"
	case PhaseSemi:
		return "Semi-final"
	case PhaseFinal:
		return "Final"
	default:
		return string(p)
	}
}

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

// Match is a single fixture. ScoreA/ScoreB are nil until the admin records a
// result; for knockouts they hold the regulation 90-minute score.
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

	// Live presentation fields, populated at read time from the in-memory live
	// feed and NEVER persisted (the store reads/writes only the columns above).
	// They stay zero-valued unless the service overlays a live snapshot, and are
	// meaningful only while Live is true (the match is in play).
	Live       bool
	LiveScoreA int
	LiveScoreB int
	LiveMinute int          // feed "period" (half number), not a clock minute; display uses LiveClock
	LiveClock  string       // display clock, e.g. "67'"
	LivePhase  string       // controlled in-play phase label ("halftime", …) from the feed; "" for ordinary play
	LiveOdds   string       // sanitized pre-match odds from the feed, e.g. "USA -160 · O/U 2.5"; empty if absent
	LiveEvents []MatchEvent // sanitized key events (goals/cards) from the feed, oldest→newest; nil if none
}

// MatchEvent is one key in-play moment from the live feed — a goal, own goal, or
// card — sanitized for display. Like the other Live* fields it is read-time only
// and NEVER persisted. Text is the feed's human-readable description (it carries
// the scorer's/player's name, since the feed's structured athlete refs are empty
// for this competition); Type is the event kind; Clock the match minute; Scoring
// marks a goal. The feed is untrusted, so every string is sanitized at the source.
type MatchEvent struct {
	Clock   string // "11'"
	Type    string // "Goal", "Own Goal", "Yellow Card"
	Text    string // "Own Goal by Cameron Burgess, Australia. USA 1, Australia 0."
	Scoring bool
}

// FormOutcome is one past result from a team's perspective, used for the bet
// screen's recent-form strip. Ordered oldest→newest in a form slice.
type FormOutcome int

const (
	FormWin FormOutcome = iota
	FormDraw
	FormLoss
)

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

// LeaderboardComment is BETanIA's persisted one-line take on a player's standing —
// the durable backing store for the in-memory comment cache (one row per user, the
// latest comment). Player is the display name the comment is about; ExpiresAt is the
// zero time for per-player comments (they never expire on a clock).
type LeaderboardComment struct {
	UserID    int64
	Player    string
	Text      string
	CreatedAt time.Time
	ExpiresAt time.Time
}
