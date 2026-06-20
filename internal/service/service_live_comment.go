package service

import (
	"fmt"
	"sort"
	"time"

	"bethoven/internal/ai"
)

// livePicksTop caps how many picks per live match we hand the commenter — the
// closest few are plenty of grounding for one line, and keeps the prompt small.
const livePicksTop = 6

// settledWindow is how long a just-finished match stays in the director's
// "react to a fresh result" buffer (see recentSettled / onMatchSettled).
const settledWindow = 10 * time.Minute

// defaultLiveLookahead is the fallback "about to kick off" window when the service
// wasn't configured with one (SetLiveLookahead).
const defaultLiveLookahead = 30 * time.Minute

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

// LiveSituation builds the snapshot the director reasons over: the in-play matches
// (score, clock, odds, closest picks), the matches about to kick off, the matches
// that just finished (with how the pool bet them), and the standings/movers. The
// bool reports whether there's ANYTHING to talk about — in play, upcoming, or just
// finished (false ⇒ the worker clears its cache). Worker seam, ungated — the worker,
// not a user, is the caller (mirrors CommentConfig / StandingsHistory).
func (s *Service) LiveSituation() (ai.LiveSituation, bool, error) {
	picks, err := s.LivePicks()
	if err != nil {
		return ai.LiveSituation{}, false, err
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

	// Upcoming: matches kicking off within the lookahead window — "a game about to
	// start". Skip anything already live (it's in `matches`) or finished.
	now := s.Now()
	lookahead := s.liveLookahead
	if lookahead <= 0 {
		lookahead = defaultLiveLookahead
	}
	horizon := now.Add(lookahead)
	fixtures, err := s.Fixtures()
	if err != nil {
		return ai.LiveSituation{}, false, err
	}
	var upcoming []ai.LiveUpcoming
	for _, m := range fixtures {
		if m.Finished || m.Live || !m.StartsAt.After(now) || m.StartsAt.After(horizon) {
			continue
		}
		upcoming = append(upcoming, ai.LiveUpcoming{
			TeamA:       m.TeamA,
			TeamB:       m.TeamB,
			Stage:       stageLabel(m),
			MinutesToKO: int(m.StartsAt.Sub(now).Minutes()),
		})
	}

	// Settled: matches that finished within the last settledWindow, with how the pool
	// bet them (final points) — "a result just in".
	settled := s.buildSettled(s.recentlySettledIDs())

	// Nothing in play, upcoming, or just-finished: tell the worker to go quiet.
	if len(matches) == 0 && len(upcoming) == 0 && len(settled) == 0 {
		return ai.LiveSituation{}, false, nil
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
	// shrinking gaps, not just whoever nailed the live scoreline. Always populated;
	// at halftime the prompt leans on it entirely (halftimeFocus in the ai package).
	standings := make([]ai.LiveStanding, 0, len(board))
	for i, st := range board {
		standings = append(standings, ai.LiveStanding{
			Player:     st.User.DisplayName,
			Position:   i + 1,
			Total:      st.Total,
			LivePoints: st.LivePoints,
		})
	}

	return ai.LiveSituation{Matches: matches, Upcoming: upcoming, Settled: settled, Movers: movers, Standings: standings}, true, nil
}

// buildSettled turns recently-finished match ids into the director's just-finished
// snapshot: the result + each player's pick and final points (top scorers first,
// capped like the live picks). Skips anything not actually settled. The live
// "story" (RecentLiveComments) is deliberately omitted — that's for the heavier
// derived-notes digest, not this fast per-tick snapshot.
func (s *Service) buildSettled(ids []int64) []ai.LiveSettled {
	if len(ids) == 0 {
		return nil
	}
	sc, err := s.newScorer()
	if err != nil {
		return nil
	}
	out := make([]ai.LiveSettled, 0, len(ids))
	for _, id := range ids {
		m, err := s.store.MatchByID(id)
		if err != nil || !m.Finished || m.ScoreA == nil || m.ScoreB == nil {
			continue
		}
		ls := ai.LiveSettled{
			TeamA: m.TeamA,
			TeamB: m.TeamB,
			Score: fmt.Sprintf("%d-%d", *m.ScoreA, *m.ScoreB),
			Stage: stageLabel(*m),
		}
		if bets, err := s.store.BetsForMatch(m.ID); err == nil && len(bets) > 0 {
			uids := make([]int64, 0, len(bets))
			for _, b := range bets {
				uids = append(uids, b.UserID)
			}
			users, _ := s.store.UsersByIDs(uids)
			picks := make([]ai.LivePickInfo, 0, len(bets))
			for _, b := range bets {
				picks = append(picks, ai.LivePickInfo{
					Player:     users[b.UserID].DisplayName,
					PredA:      b.PredA,
					PredB:      b.PredB,
					Pred:       fmt.Sprintf("%d-%d", b.PredA, b.PredB),
					LivePoints: sc.points(b, *m),
				})
			}
			// Highest scorers first, then cap — keeps the prompt small but always
			// shows who nailed it.
			sort.Slice(picks, func(i, j int) bool { return picks[i].LivePoints > picks[j].LivePoints })
			if len(picks) > livePicksTop {
				picks = picks[:livePicksTop]
			}
			ls.Picks = picks
		}
		out = append(out, ls)
	}
	return out
}
