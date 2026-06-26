package service

import (
	"time"

	"bethoven/internal/ai"
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
		}
		if !u.CreatedAt.IsZero() {
			pp.RegisteredAt = u.CreatedAt.UTC().Format("Jan 2")
		}
		out = append(out, pp)
	}
	return out
}
