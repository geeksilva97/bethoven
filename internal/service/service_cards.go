package service

import (
	"context"
	"fmt"
	"time"

	"bethoven/internal/ai"
	"bethoven/internal/models"
	"bethoven/internal/scoring"
)

// CardPoint is one round of a player's personal trajectory on their card: where
// they sat, with how many points, and how they moved that round. Mirrors
// ai.PlayerStanding but lives in the service so the TUI can draw the arc.
type CardPoint struct {
	Label        string
	Position     int
	Total        int
	Movement     int
	PointsGained int
}

// PlayerCard is one player's end-of-tournament recap. Everything but Narrative is
// recomputed at read time from the standings history + the player's results — no new
// storage. Narrative is BETanIA's persisted "hero's journey" text (empty until an
// admin generates it). Medal is 1/2/3 for the top three finishers, else 0.
type PlayerCard struct {
	User           models.User
	FinalRank      int
	Medal          int
	Total          int
	ExactHits      int
	CorrectResults int
	StartRank      int
	PeakRank       int
	BiggestClimb   int    // best single-round rise (+places); 0 if they never climbed
	ClimbRound     string // the round where the biggest climb happened
	RoundsAsLeader int    // rounds (from when they joined) spent at #1
	HitRate        int    // correct-result % of MatchesBet (0 when none bet) — skips never count against it
	BestStreak     int    // longest run of consecutive correct-result picks (a skip or miss breaks it)
	Trajectory     []CardPoint
	BestPick       *MatchResult // highest-scoring finished pick (nil if none scored)
	WorstPick      *MatchResult // a finished pick that scored 0, biggest goal-distance miss
	Narrative      string
	NarratedAt     time.Time
	IsSelf         bool // BETanIA's own card

	// Participation & tenure — a NO-PICK is not a wrong pick, and a late joiner
	// isn't accountable for games before they arrived. All over finished matches.
	MatchesAvailable     int          // finished games open to them (kicked off after they joined)
	MatchesBet           int          // of those, how many they actually predicted
	MatchesSkipped       int          // available games left with no pick (absent, not wrong)
	MatchesBeforeJoining int          // finished games played before they registered
	JoinedLate           bool         // some matches finished before they registered
	LastPick             *MatchResult // their most recent predicted finished game
	RecentSkips          int          // available games after their last pick left blank (a "gave up" tail)
	MiddleSkips          int          // available games left blank between first and last pick (in-and-out)
}

// timeouts bound the on-demand card generation (one no-web-search call per player).
const (
	cardGenAllTimeout = 30 * time.Minute // many players, one call each
	cardGenOneTimeout = 3 * time.Minute
)

// PlayerCards builds every player's end-of-tournament card, ranked by final
// standing, with any persisted BETanIA narrative overlaid. Admin only (players can't
// see cards yet). Returns an empty slice when no match has finished.
func (s *Service) PlayerCards(by *models.User) ([]PlayerCard, error) {
	if err := requireAdmin(by); err != nil {
		return nil, err
	}
	return s.buildPlayerCards()
}

// buildPlayerCards is the ungated core: it folds the standings history into a card
// per player. Shared by the admin read (PlayerCards) and the worker seams
// (CardDigests / CardDigest), so the stats are identical wherever a card is shown.
func (s *Service) buildPlayerCards() ([]PlayerCard, error) {
	history, err := s.StandingsHistory()
	if err != nil {
		return nil, err
	}
	if len(history) == 0 {
		return nil, nil
	}
	users, err := s.store.AllUsers()
	if err != nil {
		return nil, err
	}
	userByID := make(map[int64]models.User, len(users))
	for _, u := range users {
		userByID[u.ID] = u
	}
	narratives, _ := s.store.AllPlayerCards() // best-effort overlay

	final := history[len(history)-1].Ranks
	cards := make([]PlayerCard, 0, len(final))
	for _, ps := range final {
		card := PlayerCard{
			User:      userByID[ps.UserID],
			FinalRank: ps.Position,
			Total:     ps.Total,
			IsSelf:    userByID[ps.UserID].Fingerprint == ai.Fingerprint,
		}
		if ps.Position <= 3 {
			card.Medal = ps.Position
		}
		// Walk this player's trajectory + arc extremes, but only from the round they
		// JOINED (rounds before registration aren't their story — they'd otherwise show
		// up artificially last and inflate a "climb from the bottom"). Round labels are
		// UTC dates ("2006-01-02"), so they compare lexicographically.
		regDate := ""
		if !card.User.CreatedAt.IsZero() {
			regDate = card.User.CreatedAt.UTC().Format("2006-01-02")
		}
		first := true
		for _, r := range history {
			if regDate != "" && r.Label < regDate {
				continue
			}
			for _, p := range r.Ranks {
				if p.UserID != ps.UserID {
					continue
				}
				card.Trajectory = append(card.Trajectory, CardPoint{
					Label: r.Label, Position: p.Position, Total: p.Total,
					Movement: p.Movement, PointsGained: p.PointsGained,
				})
				if first {
					card.StartRank = p.Position
				}
				if card.PeakRank == 0 || p.Position < card.PeakRank {
					card.PeakRank = p.Position
				}
				if p.Position == 1 {
					card.RoundsAsLeader++
				}
				// Skip the first kept round's movement: for a late joiner it's the
				// artificial jump out of the pre-registration filler, not a real climb.
				if !first && p.Movement > card.BiggestClimb {
					card.BiggestClimb = p.Movement
					card.ClimbRound = r.Label
				}
				first = false
				break
			}
		}
		s.fillCardPicks(&card)
		if n, ok := narratives[ps.UserID]; ok {
			card.Narrative = n.Text
			card.NarratedAt = n.At
		}
		cards = append(cards, card)
	}
	return cards, nil
}

// fillCardPicks tallies the player's exact/correct hits and picks their best call
// (highest-scoring finished pick) and worst miss (a finished pick that scored 0, the
// one furthest off by goal distance) — all from finished matches only, so the card is
// the stable "as if ended" snapshot. It also folds in PARTICIPATION (a no-pick is not
// a wrong pick), TENURE (a late joiner isn't blamed for games before they arrived),
// and a GIVE-UP tail (available games left blank after their last pick). MyResults
// returns matches chronologically (ListMatches is ORDER BY starts_at), so the
// give-up tail is just the available games trailing the last bet.
func (s *Service) fillCardPicks(card *PlayerCard) {
	rows, _, err := s.MyResults(card.User.ID)
	if err != nil {
		return
	}
	reg := card.User.CreatedAt
	// Participation/tenure via the shared helper (one source of truth for the
	// availability rule, also used by the comment participation digest).
	p := computeParticipation(rows, reg)
	card.MatchesAvailable = p.Available
	card.MatchesBet = p.Bet
	card.MatchesSkipped = p.Skipped
	card.MatchesBeforeJoining = p.BeforeJoining
	card.JoinedLate = p.JoinedLate
	card.RecentSkips = p.RecentSkips
	card.MiddleSkips = p.MiddleSkips
	if p.LastBetIdx >= 0 {
		row := rows[p.LastBetIdx]
		card.LastPick = &row
	}

	// A second pass over the same rows for the SCORED picks (exact/correct/best/worst)
	// and the correct-result streak — these need the per-game iteration, and the
	// availability guard here mirrors computeParticipation's rule exactly.
	worstDist := -1
	streak := 0 // current run of consecutive correct-result picks (a skip or miss resets it)
	for i := range rows {
		r := rows[i]
		if !r.Match.Finished || r.Match.ScoreA == nil || r.Match.ScoreB == nil {
			continue
		}
		if !reg.IsZero() && !r.Match.StartsAt.After(reg) {
			continue // before they joined — not theirs to play
		}
		if r.Bet == nil {
			streak = 0 // a blank breaks a hot streak
			continue
		}
		row := r
		if scoring.IsExact(*r.Bet, r.Match) {
			card.ExactHits++
		}
		if scoring.IsCorrectResult(*r.Bet, r.Match) {
			card.CorrectResults++
			streak++
			if streak > card.BestStreak {
				card.BestStreak = streak
			}
		} else {
			streak = 0 // a wrong result ends the run
		}
		if card.BestPick == nil || r.Points > card.BestPick.Points {
			card.BestPick = &row
		}
		if r.Points == 0 {
			dist := absInt(r.Bet.PredA-*r.Match.ScoreA) + absInt(r.Bet.PredB-*r.Match.ScoreB)
			if dist > worstDist {
				worstDist = dist
				card.WorstPick = &row
			}
		}
	}
	// Accuracy is over games they ACTUALLY bet — a skip is absence, not a miss, so it
	// never drags the rate down (mirrors the no-pick-is-not-a-wrong-pick rule).
	if card.MatchesBet > 0 {
		card.HitRate = card.CorrectResults * 100 / card.MatchesBet
	}
	if card.BestPick != nil && card.BestPick.Points == 0 {
		card.BestPick = nil // nothing actually scored — don't tout a 0 as a "best call"
	}
}

// --- BETanIA worker seams (ungated; the worker, not a user, is the caller) -------

// CardDigests builds one card-digest input per player for BETanIA to narrate (the
// admin "generate all" batch). Muted players are skipped so no tokens are spent on
// them — their stats card still renders, just without a narrative.
func (s *Service) CardDigests() ([]ai.CardDigestData, error) {
	cards, err := s.buildPlayerCards()
	if err != nil {
		return nil, err
	}
	story := s.DerivedNotesText()
	muted := s.mutedNames()
	out := make([]ai.CardDigestData, 0, len(cards))
	for _, c := range cards {
		if muted[c.User.DisplayName] {
			continue
		}
		out = append(out, cardDigest(c, len(cards), story))
	}
	return out, nil
}

// CardDigest builds one player's card-digest input (the per-card regen). Errors when
// the player has no card (no finished matches / unknown id) or is muted.
func (s *Service) CardDigest(userID int64) (ai.CardDigestData, error) {
	cards, err := s.buildPlayerCards()
	if err != nil {
		return ai.CardDigestData{}, err
	}
	story := s.DerivedNotesText()
	for _, c := range cards {
		if c.User.ID != userID {
			continue
		}
		u := c.User
		if s.IsMuted(&u) {
			return ai.CardDigestData{}, fmt.Errorf("%s is muted", u.DisplayName)
		}
		return cardDigest(c, len(cards), story), nil
	}
	return ai.CardDigestData{}, fmt.Errorf("no card for that player")
}

// SavePlayerCardNarrative persists one generated narrative, stamped with the server
// clock (kept here so the injected Clock owns time, not the ai package). WORKER seam.
func (s *Service) SavePlayerCardNarrative(userID int64, narrative string) error {
	return s.store.UpsertPlayerCard(userID, narrative, s.Now())
}

// SetCardGen attaches the worker's card-generation hooks (CommentWorker.GenerateCards
// / GenerateCard). Optional — unset ⇒ the card actions report the worker is off.
func (s *Service) SetCardGen(all func(ctx context.Context) error, one func(ctx context.Context, userID int64) (string, error)) {
	s.aiCardGen = all
	s.aiCardGenOne = one
}

// --- admin actions ---------------------------------------------------------------

// GeneratePlayerCards regenerates ALL players' card narratives via BETanIA and
// persists them (the admin "generate all" key). Admin only. Synchronous — callers
// run it off the UI thread (a tea.Cmd). A per-player failure is folded into the
// returned error, but every card that succeeded is already saved.
func (s *Service) GeneratePlayerCards(by *models.User) error {
	if err := requireAdmin(by); err != nil {
		return err
	}
	if s.aiCardGen == nil {
		return ErrAIOff
	}
	ctx, cancel := context.WithTimeout(context.Background(), cardGenAllTimeout)
	defer cancel()
	err := s.aiCardGen(ctx)
	s.track(by, by.Fingerprint, EvCardGenerated, map[string]string{"scope": "all"})
	return err
}

// RegeneratePlayerCard regenerates ONE player's card narrative (the admin per-card
// "regenerate" key) and returns the new text. Admin only. Synchronous.
func (s *Service) RegeneratePlayerCard(by *models.User, userID int64) (string, error) {
	if err := requireAdmin(by); err != nil {
		return "", err
	}
	if s.aiCardGenOne == nil {
		return "", ErrAIOff
	}
	ctx, cancel := context.WithTimeout(context.Background(), cardGenOneTimeout)
	defer cancel()
	text, err := s.aiCardGenOne(ctx, userID)
	if err != nil {
		return "", err
	}
	s.track(by, by.Fingerprint, EvCardGenerated, map[string]string{"scope": "one"})
	return text, nil
}

// cardDigest converts a computed PlayerCard into the ai grounding input.
func cardDigest(c PlayerCard, totalPlayers int, story string) ai.CardDigestData {
	d := ai.CardDigestData{
		UserID:         c.User.ID,
		Player:         c.User.DisplayName,
		IsSelf:         c.IsSelf,
		FinalRank:      c.FinalRank,
		TotalPlayers:   totalPlayers,
		TotalPoints:    c.Total,
		ExactHits:      c.ExactHits,
		CorrectResults: c.CorrectResults,
		StartRank:      c.StartRank,
		PeakRank:       c.PeakRank,
		Story:          story,

		MatchesAvailable:     c.MatchesAvailable,
		MatchesBet:           c.MatchesBet,
		MatchesSkipped:       c.MatchesSkipped,
		MatchesBeforeJoining: c.MatchesBeforeJoining,
		JoinedLate:           c.JoinedLate,
		RecentSkips:          c.RecentSkips,
		MiddleSkips:          c.MiddleSkips,
	}
	if !c.User.CreatedAt.IsZero() {
		d.RegisteredAt = c.User.CreatedAt.UTC().Format("Jan 2")
	}
	if c.LastPick != nil {
		d.LastPick = c.LastPick.Match.StartsAt.UTC().Format("Jan 2")
	}
	for _, t := range c.Trajectory {
		d.Trajectory = append(d.Trajectory, ai.CardRound{
			Round: t.Label, Position: t.Position, Total: t.Total,
			Movement: t.Movement, PointsGained: t.PointsGained,
		})
	}
	d.BestPick = toCardPick(c.BestPick)
	d.WorstPick = toCardPick(c.WorstPick)
	return d
}

// toCardPick renders a result row as the model-facing pick grounding (nil-safe).
func toCardPick(mr *MatchResult) *ai.CardPick {
	if mr == nil || mr.Bet == nil {
		return nil
	}
	m := mr.Match
	actual := ""
	if m.ScoreA != nil && m.ScoreB != nil {
		actual = fmt.Sprintf("%d-%d", *m.ScoreA, *m.ScoreB)
	}
	return &ai.CardPick{
		Match:  m.TeamA + " vs " + m.TeamB,
		Stage:  string(m.Phase),
		Pred:   fmt.Sprintf("%d-%d", mr.Bet.PredA, mr.Bet.PredB),
		Actual: actual,
		Points: mr.Points,
	}
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
