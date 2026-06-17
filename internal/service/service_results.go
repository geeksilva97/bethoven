package service

import (
	"errors"
	"sort"

	"bethoven/internal/db"
	"bethoven/internal/live"
	"bethoven/internal/models"
	"bethoven/internal/scoring"
)

// MatchResult is one row of a player's "My results" view: a match, their bet on
// it (nil if none), and the points earned.
type MatchResult struct {
	Match  models.Match
	Bet    *models.Bet
	Points int
}

// Standing is one row of the leaderboard. LivePoints is the portion of Total
// that comes from matches currently in play (provisional, not final); it is 0
// when nothing the player bet on is live, and lets the UI flag the row as live.
type Standing struct {
	User       models.User
	Total      int
	LivePoints int
	// LiveRankDelta is how many places the in-play (provisional) points moved this
	// player vs. where they'd sit ignoring live matches: positive = climbed (▲),
	// negative = dropped (▼), 0 = no change / nothing live. Stateless.
	LiveRankDelta int
}

// LivePick is one player's revealed pick on an in-play match plus the
// provisional points it is currently earning.
type LivePick struct {
	User       models.User
	Bet        models.Bet
	LivePoints int
}

// LiveMatchPicks groups the revealed picks for a single in-play match.
type LiveMatchPicks struct {
	Match models.Match // live fields overlaid
	Picks []LivePick   // sorted by live points desc, then display name
}

// LivePicks reveals every player's pick for matches currently in play, with the
// provisional points each is earning. Open to all players (no public_bets gate):
// in-play matches have already kicked off, so this never exposes a blind pick —
// it mirrors the kickoff reveal rule used by MatchLeaderboard / PublicBetsGrid.
// Returns nil when no feed is attached or nothing is live.
func (s *Service) LivePicks() ([]LiveMatchPicks, error) {
	snap := s.liveSnapshot()
	if snap == nil {
		return nil, nil
	}
	matches, err := s.store.ListMatches(s.tournamentID)
	if err != nil {
		return nil, err
	}
	users, err := s.store.AllUsers()
	if err != nil {
		return nil, err
	}
	userByID := make(map[int64]models.User, len(users))
	for _, u := range users {
		userByID[u.ID] = u
	}
	sc, err := s.newScorer()
	if err != nil {
		return nil, err
	}
	var out []LiveMatchPicks
	for _, m := range matches {
		ls, ok := snap[m.ID]
		// In-play only, and re-check the kickoff guard so a stray feed entry can
		// never reveal an unstarted match's picks.
		if !ok || ls.State != live.StateIn || s.Now().Before(m.StartsAt) {
			continue
		}
		overlayLive(&m, snap)
		bets, err := s.store.BetsForMatch(m.ID)
		if err != nil {
			return nil, err
		}
		// Synthetic finished match at the live score, so the active scorer yields
		// the same provisional points the leaderboard folds in.
		prov := m
		a, b := ls.A, ls.B
		prov.Finished, prov.ScoreA, prov.ScoreB = true, &a, &b
		picks := make([]LivePick, 0, len(bets))
		for _, bet := range bets {
			u, ok := userByID[bet.UserID]
			if !ok {
				continue
			}
			picks = append(picks, LivePick{User: u, Bet: bet, LivePoints: sc.points(bet, prov)})
		}
		sort.Slice(picks, func(i, j int) bool {
			if picks[i].LivePoints != picks[j].LivePoints {
				return picks[i].LivePoints > picks[j].LivePoints
			}
			return picks[i].User.DisplayName < picks[j].User.DisplayName
		})
		out = append(out, LiveMatchPicks{Match: m, Picks: picks})
	}
	return out, nil
}

// MatchStanding is one row of a per-match ranking: a player, their bet on that
// match (nil if none), the points it earned, and the breakdown of how those
// points were computed (for the drill-down view).
type MatchStanding struct {
	User      models.User
	Bet       *models.Bet
	Points    int
	Breakdown scoring.Breakdown
}

// Fixtures lists the active tournament's matches in kickoff order (for the
// fixtures/betting screen), with live scores overlaid for in-play matches.
func (s *Service) Fixtures() ([]models.Match, error) {
	matches, err := s.store.ListMatches(s.tournamentID)
	if err != nil {
		return nil, err
	}
	snap := s.liveSnapshot()
	for i := range matches {
		overlayLive(&matches[i], snap)
	}
	return matches, nil
}

// LiveMatches returns only the matches currently in play (with live scores
// overlaid), for the leaderboard's "in play" header. Empty when nothing is live.
func (s *Service) LiveMatches() ([]models.Match, error) {
	matches, err := s.store.ListMatches(s.tournamentID)
	if err != nil {
		return nil, err
	}
	snap := s.liveSnapshot()
	out := make([]models.Match, 0)
	for i := range matches {
		overlayLive(&matches[i], snap)
		if matches[i].Live {
			out = append(out, matches[i])
		}
	}
	return out, nil
}

// FinalizeFromFeed records the official result reported by the live feed. Unlike
// EnterResult it is NOT admin-gated — the server itself is the caller — but it
// only writes when the match is not already finished, so it never clobbers a
// settled result or an admin correction. The admin override path (EnterResult)
// overwrites unconditionally and wins.
func (s *Service) FinalizeFromFeed(matchID int64, scoreA, scoreB int) error {
	if scoreA < 0 || scoreA > 99 || scoreB < 0 || scoreB > 99 {
		return ErrInvalidScore
	}
	m, err := s.store.MatchByID(matchID)
	if errors.Is(err, db.ErrNotFound) {
		return ErrMatchNotFound
	}
	if err != nil {
		return err
	}
	if m.Finished {
		return nil // already settled (by an earlier poll or by an admin) — leave it
	}
	// Conditional write: if an admin's EnterResult landed between the read above
	// and here, finished=1 already and this is a no-op, so the feed never
	// clobbers an admin correction (admin always wins).
	_, err = s.store.SetResultIfUnfinished(matchID, scoreA, scoreB)
	return err
}

// MyResults returns the player's per-match results plus their running total.
// Matches the player never bet on appear with a nil Bet and 0 points.
func (s *Service) MyResults(userID int64) ([]MatchResult, int, error) {
	matches, err := s.store.ListMatches(s.tournamentID)
	if err != nil {
		return nil, 0, err
	}
	bets, err := s.store.BetsForUser(userID, s.tournamentID)
	if err != nil {
		return nil, 0, err
	}
	byMatch := make(map[int64]models.Bet, len(bets))
	for _, b := range bets {
		byMatch[b.MatchID] = b
	}
	sc, err := s.newScorer()
	if err != nil {
		return nil, 0, err
	}

	snap := s.liveSnapshot()
	out := make([]MatchResult, 0, len(matches))
	total := 0
	for _, m := range matches {
		overlayLive(&m, snap)
		row := MatchResult{Match: m}
		if b, ok := byMatch[m.ID]; ok {
			bcopy := b
			row.Bet = &bcopy
			row.Points = sc.points(b, m)
			total += row.Points
		}
		out = append(out, row)
	}
	return out, total, nil
}

// MatchLeaderboard ranks every player by the points they earned on a single
// match — the "who nailed this game" view. Players who bet on the match are
// ranked by points (desc) then name; players who didn't bet are omitted.
//
// This view exposes individual picks, so it is gated server-side: until the
// match has a recorded result, it returns the match with NO rows — picks are
// never fetched across the trust boundary for an unstarted/ongoing game. (The
// TUI also hides them, as defense in depth, but the rule lives here.)
func (s *Service) MatchLeaderboard(matchID int64) (*models.Match, []MatchStanding, error) {
	m, err := s.store.MatchByID(matchID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil, ErrMatchNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	overlayLive(m, s.liveSnapshot()) // show the running score in the header

	// Gate: don't reveal anyone's picks before the match has a result. The live
	// score (set above) is fine to show — it's not a pick.
	if !m.Finished {
		return m, nil, nil
	}

	bets, err := s.store.BetsForMatch(matchID)
	if err != nil {
		return nil, nil, err
	}
	if len(bets) == 0 {
		return m, nil, nil
	}

	userIDs := make([]int64, 0, len(bets))
	for _, b := range bets {
		userIDs = append(userIDs, b.UserID)
	}
	users, err := s.store.UsersByIDs(userIDs)
	if err != nil {
		return nil, nil, err
	}
	sc, err := s.newScorer()
	if err != nil {
		return nil, nil, err
	}

	rows := make([]MatchStanding, 0, len(bets))
	for _, b := range bets {
		bcopy := b
		rows = append(rows, MatchStanding{
			User:      users[b.UserID],
			Bet:       &bcopy,
			Points:    sc.points(b, *m),
			Breakdown: sc.explain(b, *m),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Points != rows[j].Points {
			return rows[i].Points > rows[j].Points
		}
		return rows[i].User.DisplayName < rows[j].User.DisplayName
	})
	return m, rows, nil
}

// Leaderboard returns every player's total, sorted by points (desc) then name.
func (s *Service) Leaderboard() ([]Standing, error) {
	users, err := s.store.AllUsers()
	if err != nil {
		return nil, err
	}
	matches, err := s.store.ListMatches(s.tournamentID)
	if err != nil {
		return nil, err
	}
	matchByID := make(map[int64]models.Match, len(matches))
	for _, m := range matches {
		matchByID[m.ID] = m
	}
	bets, err := s.store.AllBets(s.tournamentID)
	if err != nil {
		return nil, err
	}
	sc, err := s.newScorer()
	if err != nil {
		return nil, err
	}
	snap := s.liveSnapshot()
	totals := make(map[int64]int)
	livePts := make(map[int64]int) // provisional points from in-play matches
	for _, b := range bets {
		m, ok := matchByID[b.MatchID]
		if !ok {
			continue
		}
		if m.Finished {
			totals[b.UserID] += sc.points(b, m)
			continue
		}
		// In-play match: score the bet against the current live score by treating
		// it as if final — a synthetic match keeps the scorer untouched.
		if ls, inPlay := snap[b.MatchID]; inPlay && ls.State == live.StateIn {
			prov := m
			a, bb := ls.A, ls.B
			prov.Finished, prov.ScoreA, prov.ScoreB = true, &a, &bb
			p := sc.points(b, prov)
			totals[b.UserID] += p
			livePts[b.UserID] += p
		}
	}

	standings := make([]Standing, 0, len(users))
	for _, u := range users {
		standings = append(standings, Standing{User: u, Total: totals[u.ID], LivePoints: livePts[u.ID]})
	}
	sort.Slice(standings, func(i, j int) bool {
		if standings[i].Total != standings[j].Total {
			return standings[i].Total > standings[j].Total
		}
		return standings[i].User.DisplayName < standings[j].User.DisplayName
	})

	// Rank movement caused by the in-play (provisional) points: compare each
	// player's current rank against where they'd sit on settled points only
	// (Total - LivePoints), using the same tie-break so only the live delta moves
	// anyone. With nothing live, settled == current and every delta is 0.
	settled := make([]Standing, len(standings))
	copy(settled, standings)
	sort.Slice(settled, func(i, j int) bool {
		si, sj := settled[i].Total-settled[i].LivePoints, settled[j].Total-settled[j].LivePoints
		if si != sj {
			return si > sj
		}
		return settled[i].User.DisplayName < settled[j].User.DisplayName
	})
	settledRank := make(map[int64]int, len(settled))
	for i, s := range settled {
		settledRank[s.User.ID] = i + 1
	}
	for i := range standings {
		standings[i].LiveRankDelta = settledRank[standings[i].User.ID] - (i + 1)
	}
	return standings, nil
}
