package service

import (
	"fmt"
	"time"

	"bethoven/internal/ai"
)

// livePicksTop caps how many picks per live match we hand the commenter — the
// closest few are plenty of grounding for one line, and keeps the prompt small.
const livePicksTop = 6

// LiveCommentSource exposes BETanIA's current live-commentary line (the in-memory
// cache the live-comment worker writes). nil is valid and means the worker isn't
// running, in which case the leaderboard shows no live line. Mirrors CommentSource.
type LiveCommentSource interface {
	Current(now time.Time) string
}

// SetLiveCommentSource attaches the live-comment cache. Optional — unset ⇒ no line.
func (s *Service) SetLiveCommentSource(src LiveCommentSource) { s.liveComments = src }

// LiveCommentary returns BETanIA's current top-of-leaderboard live line, or "" when
// nothing is live, no worker is attached, or the line has expired. Ungated, like
// AllLeaderboardComments: it's a shared, fun line about the live slate, not a pick
// (bets stay private through a separate boundary).
func (s *Service) LiveCommentary() string {
	if s.liveComments == nil {
		return ""
	}
	return s.liveComments.Current(s.Now())
}

// settingLiveCommentState is the KV key holding the live-comment cache snapshot
// (BETanIA's current line + anti-repeat history) so a version swap mid-game doesn't
// blank it. It's throwaway state, but the DB keeps it consistent with the rest
// (WAL-safe, atomic, one store) instead of a loose file — the ai package marshals
// the JSON, the service just persists the string.
const settingLiveCommentState = "live_comment_state"

// SaveLiveCommentState persists the live-comment snapshot (an opaque JSON string
// produced by ai.LiveCommentCache.SnapshotJSON). Called from main on shutdown.
// An empty snapshot clears the row. Worker/lifecycle seam — not admin-gated.
func (s *Service) SaveLiveCommentState(snapshot string) error {
	return s.store.SetSetting(settingLiveCommentState, snapshot)
}

// LoadLiveCommentState returns the last persisted live-comment snapshot (or "" when
// none), for main to hand to ai.LiveCommentCache.LoadJSON at boot.
func (s *Service) LoadLiveCommentState() string {
	v, err := s.store.GetSetting(settingLiveCommentState)
	if err != nil {
		return ""
	}
	return v
}

// LiveSituation builds the snapshot the live-comment worker reasons over: the
// in-play matches (score, clock, odds, and the closest picks) plus the players
// whose standing is shifting on the provisional points. The bool reports whether
// anything is live (false ⇒ the worker clears its cache). Worker seam, ungated —
// the worker, not a user, is the caller (mirrors CommentConfig / StandingsHistory).
func (s *Service) LiveSituation() (ai.LiveSituation, bool, error) {
	picks, err := s.LivePicks()
	if err != nil {
		return ai.LiveSituation{}, false, err
	}
	if len(picks) == 0 {
		return ai.LiveSituation{}, false, nil // nothing in play
	}

	matches := make([]ai.LiveMatchInfo, 0, len(picks))
	for _, mp := range picks {
		info := ai.LiveMatchInfo{
			TeamA:  mp.Match.TeamA,
			TeamB:  mp.Match.TeamB,
			ScoreA: mp.Match.LiveScoreA,
			ScoreB: mp.Match.LiveScoreB,
			Clock:  mp.Match.LiveClock,
			Phase:  mp.Match.LivePhase,
			Odds:   mp.Match.LiveOdds,
		}
		// Key events (goals/cards) so BETanIA can name the scorer; already sanitized
		// and capped at the feed boundary, oldest→newest.
		for _, ev := range mp.Match.LiveEvents {
			info.Events = append(info.Events, ai.LiveEventInfo{
				Clock: ev.Clock,
				Type:  ev.Type,
				Text:  ev.Text,
			})
		}
		// Picks arrive sorted by provisional points desc — take the closest few.
		for i, pk := range mp.Picks {
			if i >= livePicksTop {
				break
			}
			info.Picks = append(info.Picks, ai.LivePickInfo{
				Player:     pk.User.DisplayName,
				PredA:      pk.Bet.PredA,
				PredB:      pk.Bet.PredB,
				Pred:       fmt.Sprintf("%d-%d", pk.Bet.PredA, pk.Bet.PredB),
				LivePoints: pk.LivePoints,
			})
		}
		matches = append(matches, info)
	}

	// Movers: anyone gaining provisional points or whose rank shifted because of it.
	board, err := s.Leaderboard()
	if err != nil {
		return ai.LiveSituation{}, false, err
	}
	var movers []ai.LiveMover
	for _, st := range board {
		if st.LivePoints == 0 && st.LiveRankDelta == 0 {
			continue
		}
		movers = append(movers, ai.LiveMover{
			Player:     st.User.DisplayName,
			RankDelta:  st.LiveRankDelta,
			LivePoints: st.LivePoints,
		})
	}

	// Overall standings (rank-sorted) so the line can talk about the title race and
	// shrinking gaps, not just whoever nailed the live scoreline.
	standings := make([]ai.LiveStanding, 0, len(board))
	for i, st := range board {
		standings = append(standings, ai.LiveStanding{
			Player:     st.User.DisplayName,
			Position:   i + 1,
			Total:      st.Total,
			LivePoints: st.LivePoints,
		})
	}

	return ai.LiveSituation{Matches: matches, Movers: movers, Standings: standings}, true, nil
}
