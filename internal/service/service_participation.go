package service

import (
	"time"

	"bethoven/internal/ai"
	"bethoven/internal/models"
)

// Defector thresholds. A defector is a player who PLAYED and then abandoned the pool
// down the stretch — a real trailing give-up (RecentSkips), NOT a late join or a
// never-start — once the tournament reached its business end. Kept named so they're
// easy to tune in one place.
const (
	defectorMinTail  = 3  // trailing available games left blank after the last pick
	defectorLateFrac = 70 // percent of the schedule finished that counts as "the end"
)

// participation is one player's read-time participation/tenure summary over finished
// matches: how many were open to them (after they joined), how many they actually
// bet, how many they left blank, whether they joined late, and a "gave up" tail.
// A no-pick is ABSENCE, not a wrong pick — this is what lets the card AND BETanIA's
// commentary tell the two apart. Computed, never stored.
type participation struct {
	Available     int
	Bet           int
	Skipped       int
	BeforeJoining int
	RecentSkips   int // available games left blank AFTER the last pick (a "gave up" tail)
	MiddleSkips   int // available games left blank BETWEEN the first and last pick (in-and-out)
	JoinedLate    bool
	NeverPicked   bool
	LastBetIdx    int // index into the rows of the most recent finished bet, -1 if none
}

// computeParticipation folds a player's chronological results (all matches, finished
// or not — MyResults order) against their registration time. A match that kicked off
// before they registered was never theirs to bet (BeforeJoining); of the games open to
// them, a nil Bet is a skip. RecentSkips is the run of available games left blank after
// their last pick. The single source of truth for the availability rule, shared by the
// card (fillCardPicks) and the comment participation digest.
func computeParticipation(rows []MatchResult, reg time.Time) participation {
	p := participation{LastBetIdx: -1}
	availIdxLastBet := -1   // 1-based index, among available games, of the last one bet
	firstBetSeen := false   // have we passed their first pick yet?
	skipsAfterFirstBet := 0 // blanks once they'd started picking (middle + trailing)
	for i := range rows {
		r := rows[i]
		if !r.Match.Finished || r.Match.ScoreA == nil || r.Match.ScoreB == nil {
			continue
		}
		// Open to them only if they'd registered before kickoff; a game already underway
		// when they joined was never theirs to bet (the kickoff lock guarantees no bet).
		if !reg.IsZero() && !r.Match.StartsAt.After(reg) {
			p.BeforeJoining++
			continue
		}
		p.Available++
		if r.Bet == nil {
			p.Skipped++ // a NO-PICK — absent, not a wrong prediction
			if firstBetSeen {
				skipsAfterFirstBet++
			}
			continue
		}
		p.Bet++
		firstBetSeen = true
		p.LastBetIdx = i
		availIdxLastBet = p.Available
	}
	p.JoinedLate = p.BeforeJoining > 0
	p.NeverPicked = p.Bet == 0
	// A give-up tail only means something once they'd started picking; a player who
	// never bet at all is "never started", not "gave up".
	if availIdxLastBet > 0 {
		p.RecentSkips = p.Available - availIdxLastBet
	}
	// Skips between their first and last pick (in-and-out): the blanks after they
	// started, minus the trailing give-up tail.
	p.MiddleSkips = skipsAfterFirstBet - p.RecentSkips
	return p
}

// participationDigest builds the per-player participation grounding for BETanIA's
// per-player roasts and live commentary — but ONLY for players with something to
// caveat (a skip, a late join, or never having picked). A player who bet every
// available game is omitted, so a fully-participating pool yields an empty slice and
// no participation block in either prompt (zero change for existing pools). Ungated
// worker helper, like CommentConfig.
func (s *Service) participationDigest() []ai.PlayerParticipation {
	users, err := s.store.AllUsers()
	if err != nil {
		return nil
	}
	// "The end is approaching?" — computed once for the whole pool, so a trailing tail
	// is only branded desertion in the tournament's business end (else an early-round
	// blip on someone who might still return would read as a defection).
	late := s.tournamentLate()
	var out []ai.PlayerParticipation
	for _, u := range users {
		rows, _, err := s.MyResults(u.ID)
		if err != nil {
			continue
		}
		p := computeParticipation(rows, u.CreatedAt)
		if p.Skipped == 0 && !p.JoinedLate && !p.NeverPicked {
			continue // bet everything open to them — nothing to clarify
		}
		pp := ai.PlayerParticipation{
			Name:                 u.DisplayName,
			MatchesAvailable:     p.Available,
			MatchesBet:           p.Bet,
			MatchesSkipped:       p.Skipped,
			NeverPicked:          p.NeverPicked,
			JoinedLate:           p.JoinedLate,
			MatchesBeforeJoining: p.BeforeJoining,
			RecentSkips:          p.RecentSkips,
			MiddleSkips:          p.MiddleSkips,
			// Played, then abandoned the pool down the stretch. Late joiner who then
			// bailed still counts (Bet > 0); a never-start (Bet == 0) is sitting out,
			// not quitting, so it's excluded.
			Defector: late && p.RecentSkips >= defectorMinTail && p.Bet > 0,
		}
		if !u.CreatedAt.IsZero() {
			pp.RegisteredAt = u.CreatedAt.UTC().Format("Jan 2")
		}
		out = append(out, pp)
	}
	return out
}

// tournamentLate reports whether the tournament has reached its business end — the
// "when the finish line is approaching" gate for branding a trailing give-up a
// desertion. True once any knockout match has kicked off (a non-group game whose
// StartsAt has passed) OR at least defectorLateFrac% of the schedule has finished.
// Best-effort: a store error reads as "not late" (no false accusations).
func (s *Service) tournamentLate() bool {
	matches, err := s.store.ListMatches(s.tournamentID)
	if err != nil || len(matches) == 0 {
		return false
	}
	now := s.clock.Now().UTC()
	total, finished := 0, 0
	for _, m := range matches {
		total++
		if m.Finished {
			finished++
		}
		// A knockout game that's already kicked off ⇒ we're in the closing stretch.
		if m.Phase != models.PhaseGroup && !m.StartsAt.After(now) {
			return true
		}
	}
	return finished*100 >= total*defectorLateFrac
}
