// Package achievements computes the pool's badges as pure functions, mirroring
// internal/scoring: no I/O, no DB dependency, exhaustively unit-tested. The
// service builds the Input from persisted bets + results + standings history and
// calls Compute at read time — nothing here is ever stored, so badges re-derive
// (and can change hands) whenever a result lands or is edited.
//
// Two kinds of badge:
//
//   - Superlative: one holder in the pool ("most exact scores"). Ties share the
//     badge. Each has a minimum so a two-bet fluke can't claim it (same spirit
//     as scoring's scarcityQuorum) — below the minimum it stays unclaimed.
//   - Threshold: everyone who meets the criterion wears it ("a perfect round").
//
// Every criterion reads finished matches only (the caller guarantees Picks are
// finished), so nothing here can leak a pick before kickoff.
package achievements

import (
	"fmt"
	"time"
)

// Kind separates one-holder superlatives from anyone-who-qualifies thresholds.
type Kind int

const (
	Superlative Kind = iota
	Threshold
)

// Badge is one catalog entry. ID is the stable slug criteria dispatch on.
type Badge struct {
	ID    string
	Name  string
	Emoji string
	Desc  string
	Kind  Kind
}

// Tunable thresholds. Kept named so they are easy to find and adjust.
const (
	minOracleExacts = 2                // exact scores to hold The Oracle
	minStreak       = 3                // consecutive correct results to hold Longest Streak
	minHotHand      = 2                // consecutive exact scores for Hot Hand
	minMovement     = 2                // places climbed/dropped in one round for Comeback / Free Fall
	minDraws        = 2                // correct draw calls for Draw Whisperer
	minGoalPicks    = 5                // picks before an average-goals badge means anything
	minTimingPicks  = 3                // qualifying picks before a timing badge means anything
	minEdits        = 3                // edited picks to hold Second-Guesser
	perfectRoundMin = 3                // picks in a round before Perfect Round / Blackout count
	everPresentMin  = 10               // available matches before Ever-Present means anything
	wireMinRounds   = 5                // rounds led to hold Wire-to-Wire
	quitterMinTail  = 3                // trailing skips that brand a Quitter (mirrors service defectorMinTail)
	deadlineWindow  = 10 * time.Minute // "last minute" for Deadline Junkie
	earlyLead       = 48 * time.Hour   // "way ahead" for Early Bird
	editEpsilon     = time.Second      // updated_at−created_at above this counts as an edit
	contrarianShare = 0.25             // result picked by under this fraction of the match's bettors
)

// The catalog. Order here is display order everywhere (Trophy Room, cards).
var (
	Oracle         = Badge{ID: "oracle", Name: "The Oracle", Emoji: "🔮", Desc: "most exact scores", Kind: Superlative}
	LongestStreak  = Badge{ID: "streak", Name: "Longest Streak", Emoji: "🔥", Desc: "longest run of correct results", Kind: Superlative}
	TopRound       = Badge{ID: "top-round", Name: "Top Round", Emoji: "💥", Desc: "most points in a single round", Kind: Superlative}
	Comeback       = Badge{ID: "comeback", Name: "The Comeback", Emoji: "🧗", Desc: "biggest single-round climb", Kind: Superlative}
	FreeFall       = Badge{ID: "free-fall", Name: "Free Fall", Emoji: "🪂", Desc: "biggest single-round drop", Kind: Superlative}
	DrawWhisperer  = Badge{ID: "draw-whisperer", Name: "Draw Whisperer", Emoji: "🤝", Desc: "most draws called", Kind: Superlative}
	GoalMerchant   = Badge{ID: "goal-merchant", Name: "Goal Merchant", Emoji: "⚽", Desc: "highest predicted goals per pick", Kind: Superlative}
	Accountant     = Badge{ID: "accountant", Name: "The Accountant", Emoji: "🧾", Desc: "lowest predicted goals per pick", Kind: Superlative}
	DeadlineJunkie = Badge{ID: "deadline-junkie", Name: "Deadline Junkie", Emoji: "⏱", Desc: "most picks inside 10 minutes of kickoff", Kind: Superlative}
	EarlyBird      = Badge{ID: "early-bird", Name: "Early Bird", Emoji: "🌅", Desc: "most picks placed 48h+ ahead", Kind: Superlative}
	SecondGuesser  = Badge{ID: "second-guesser", Name: "Second-Guesser", Emoji: "🔁", Desc: "most edited picks", Kind: Superlative}
	Contrarian     = Badge{ID: "contrarian", Name: "The Contrarian", Emoji: "🃏", Desc: "most points from picks few others made", Kind: Superlative}

	PerfectRound = Badge{ID: "perfect-round", Name: "Perfect Round", Emoji: "✨", Desc: "every pick in a round scored", Kind: Threshold}
	Blackout     = Badge{ID: "blackout", Name: "The Blackout", Emoji: "🕳", Desc: "a full round without a single point", Kind: Threshold}
	HotHand      = Badge{ID: "hot-hand", Name: "Hot Hand", Emoji: "🎯", Desc: "back-to-back exact scores", Kind: Threshold}
	EverPresent  = Badge{ID: "ever-present", Name: "Ever-Present", Emoji: "📅", Desc: "bet every available match", Kind: Threshold}
	Quitter      = Badge{ID: "quitter", Name: "The Quitter", Emoji: "🚪", Desc: "abandoned the pool down the stretch", Kind: Threshold}
	WireToWire   = Badge{ID: "wire-to-wire", Name: "Wire-to-Wire", Emoji: "🚂", Desc: "led every single round", Kind: Threshold}
)

// Catalog is the fixed display order: superlatives, then thresholds.
var Catalog = []Badge{
	Oracle, LongestStreak, TopRound, Comeback, FreeFall, DrawWhisperer,
	GoalMerchant, Accountant, DeadlineJunkie, EarlyBird, SecondGuesser, Contrarian,
	PerfectRound, Blackout, HotHand, EverPresent, Quitter, WireToWire,
}

// Pick is one finished match from a player's chronological history. A Skipped
// pick (no bet) carries only Round/Kickoff and exists so streaks break on a
// blank, mirroring the card's BestStreak rule.
type Pick struct {
	Round          string // round key: UTC kickoff date, as in StandingsHistory
	Skipped        bool   // no bet on this finished match
	PredA, PredB   int
	ScoreA, ScoreB int
	Points         int  // active-mode scorer output — badges agree with the board
	Exact, Correct bool // scoring.IsExact / IsCorrectResult, mode-agnostic

	// Timing, from the bet row. A pick placed at/after kickoff (ai-seed or the
	// place-bet escape hatch) has no meaningful timing and never counts for the
	// timing badges — see timingValid.
	PlacedAt, UpdatedAt, Kickoff time.Time

	// ResultShare is the fraction of the match's bettors who picked the same
	// W/D/L result (including this one). -1 when the match is below the
	// contrarian quorum — such picks never count for The Contrarian.
	ResultShare float64
}

// RoundDelta is one round of the player's trajectory, from StandingsHistory,
// starting at the round they joined. Movement on the first entry is ignored
// (a late joiner's jump out of the pre-registration filler isn't a climb) —
// the same rule the player card applies.
type RoundDelta struct {
	Label        string
	Position     int
	Movement     int // + climbed, − fell
	PointsGained int
}

// Participation is the player's computeParticipation summary, passed through so
// Ever-Present and The Quitter reuse the service's availability rule verbatim.
type Participation struct {
	Available   int
	Bet         int
	RecentSkips int // available games left blank after the last pick
}

// PlayerInput is one player's flattened history. The service builds it;
// Compute never reads the DB.
type PlayerInput struct {
	UserID int64
	Name   string
	IsAI   bool // BETanIA: ineligible for timing badges (her seed backfill makes timing nonsense)
	Picks  []Pick
	Rounds []RoundDelta
	Part   Participation
}

// Input is the whole pool. TournamentLate gates The Quitter, mirroring the
// defector rule: a trailing tail is only branded desertion in the business end.
type Input struct {
	Players        []PlayerInput
	TournamentLate bool
}

// Award is one badge earned by one player. Detail is the human line behind it
// ("4 exact scores"); Value is the number that won it, used for superlative
// ranking (higher is better — inverted metrics pre-negate).
type Award struct {
	Badge  Badge
	Detail string
	Value  int
}

// Holder is an Award pinned to its player, for the per-badge standings.
type Holder struct {
	UserID int64
	Name   string
	Detail string
	Value  int
}

// BadgeStanding is one catalog row: the badge and its current holder(s).
// No holders ⇒ unclaimed (superlative under its minimum, threshold unmet).
type BadgeStanding struct {
	Badge   Badge
	Holders []Holder
}

// Board is the full computed state: every badge in catalog order plus the
// per-player awards (the card's badge row).
type Board struct {
	Standings []BadgeStanding
	ByUser    map[int64][]Award
}

// Compute evaluates the whole catalog over the pool. Pure: same input, same
// board.
func Compute(in Input) Board {
	tallies := make([]tally, len(in.Players))
	for i := range in.Players {
		tallies[i] = tallyPlayer(in.Players[i])
	}

	board := Board{ByUser: make(map[int64][]Award)}
	for _, b := range Catalog {
		st := BadgeStanding{Badge: b}
		for i := range in.Players {
			p := in.Players[i]
			aw, ok := award(b, p, tallies[i], in.TournamentLate)
			if !ok {
				continue
			}
			if b.Kind == Superlative {
				// Keep only the best; a tie shares the badge.
				if len(st.Holders) > 0 && aw.Value < st.Holders[0].Value {
					continue
				}
				if len(st.Holders) > 0 && aw.Value > st.Holders[0].Value {
					st.Holders = st.Holders[:0]
				}
			}
			st.Holders = append(st.Holders, Holder{UserID: p.UserID, Name: p.Name, Detail: aw.Detail, Value: aw.Value})
		}
		for _, h := range st.Holders {
			board.ByUser[h.UserID] = append(board.ByUser[h.UserID], Award{Badge: b, Detail: h.Detail, Value: h.Value})
		}
		board.Standings = append(board.Standings, st)
	}
	return board
}

// tally is one player's fold over their picks + trajectory: every number a
// badge criterion needs, computed in a single pass.
type tally struct {
	exacts, correctDraws               int
	bestCorrectStreak, bestExactStreak int
	goalsTotal, betCount               int // over non-skipped picks
	lateBets, earlyBets, edits         int
	contrarianPts                      int
	perfectRounds, blackoutRounds      []string
	topRoundPts                        int
	topRoundLabel                      string
	bestClimb, worstFall               int
	climbLabel, fallLabel              string
	ledAll                             bool
}

func tallyPlayer(p PlayerInput) tally {
	var t tally
	curCorrect, curExact := 0, 0
	type roundAgg struct {
		label        string
		bets, scored int
	}
	var rounds []roundAgg
	roundIdx := make(map[string]int)

	for _, pk := range p.Picks {
		if pk.Skipped {
			curCorrect, curExact = 0, 0 // a blank breaks a hot streak
			continue
		}
		t.betCount++
		t.goalsTotal += pk.PredA + pk.PredB
		if pk.Exact {
			t.exacts++
			curExact++
			if curExact > t.bestExactStreak {
				t.bestExactStreak = curExact
			}
		} else {
			curExact = 0
		}
		if pk.Correct {
			curCorrect++
			if curCorrect > t.bestCorrectStreak {
				t.bestCorrectStreak = curCorrect
			}
			if pk.ScoreA == pk.ScoreB {
				t.correctDraws++
			}
		} else {
			curCorrect = 0
		}
		if pk.ResultShare >= 0 && pk.ResultShare < contrarianShare && pk.Points > 0 {
			t.contrarianPts += pk.Points
		}
		if timingValid(pk) {
			lead := pk.Kickoff.Sub(pk.PlacedAt)
			if lead < deadlineWindow {
				t.lateBets++
			}
			if lead > earlyLead {
				t.earlyBets++
			}
			// An edit only counts when made before kickoff too — a post-kickoff
			// correction is the escape hatch, not second-guessing.
			if pk.UpdatedAt.Sub(pk.PlacedAt) > editEpsilon && pk.UpdatedAt.Before(pk.Kickoff) {
				t.edits++
			}
		}
		i, ok := roundIdx[pk.Round]
		if !ok {
			i = len(rounds)
			roundIdx[pk.Round] = i
			rounds = append(rounds, roundAgg{label: pk.Round})
		}
		rounds[i].bets++
		if pk.Points > 0 {
			rounds[i].scored++
		}
	}
	for _, r := range rounds {
		if r.bets < perfectRoundMin {
			continue
		}
		switch r.scored {
		case r.bets:
			t.perfectRounds = append(t.perfectRounds, r.label)
		case 0:
			t.blackoutRounds = append(t.blackoutRounds, r.label)
		}
	}

	t.ledAll = len(p.Rounds) > 0
	for i, r := range p.Rounds {
		if r.Position != 1 {
			t.ledAll = false
		}
		if r.PointsGained > t.topRoundPts {
			t.topRoundPts = r.PointsGained
			t.topRoundLabel = r.Label
		}
		if i == 0 {
			continue // first joined round: movement is the join jump, not a climb
		}
		if r.Movement > t.bestClimb {
			t.bestClimb = r.Movement
			t.climbLabel = r.Label
		}
		if -r.Movement > t.worstFall {
			t.worstFall = -r.Movement
			t.fallLabel = r.Label
		}
	}
	return t
}

// award evaluates one badge for one player. ok=false means they don't qualify
// (or, for a superlative, fall under its minimum).
func award(b Badge, p PlayerInput, t tally, late bool) (Award, bool) {
	yes := func(detail string, value int) (Award, bool) {
		return Award{Badge: b, Detail: detail, Value: value}, true
	}
	no := func() (Award, bool) { return Award{}, false }

	switch b.ID {
	case Oracle.ID:
		if t.exacts >= minOracleExacts {
			return yes(plural(t.exacts, "exact score"), t.exacts)
		}
	case LongestStreak.ID:
		if t.bestCorrectStreak >= minStreak {
			return yes(fmt.Sprintf("%d correct results in a row", t.bestCorrectStreak), t.bestCorrectStreak)
		}
	case TopRound.ID:
		if t.topRoundPts > 0 {
			return yes(fmt.Sprintf("+%d pts on %s", t.topRoundPts, t.topRoundLabel), t.topRoundPts)
		}
	case Comeback.ID:
		if t.bestClimb >= minMovement {
			return yes(fmt.Sprintf("+%d places on %s", t.bestClimb, t.climbLabel), t.bestClimb)
		}
	case FreeFall.ID:
		if t.worstFall >= minMovement {
			return yes(fmt.Sprintf("-%d places on %s", t.worstFall, t.fallLabel), t.worstFall)
		}
	case DrawWhisperer.ID:
		if t.correctDraws >= minDraws {
			return yes(plural(t.correctDraws, "draw called", "draws called"), t.correctDraws)
		}
	case GoalMerchant.ID:
		if t.betCount >= minGoalPicks {
			// Scaled ×100 so the superlative compare stays integer.
			return yes(avgGoals(t), t.goalsTotal*100/t.betCount)
		}
	case Accountant.ID:
		if t.betCount >= minGoalPicks {
			// Lowest wins: negate so "higher Value is better" holds.
			return yes(avgGoals(t), -(t.goalsTotal * 100 / t.betCount))
		}
	case DeadlineJunkie.ID:
		if !p.IsAI && t.lateBets >= minTimingPicks {
			return yes(fmt.Sprintf("%d picks inside 10 min of kickoff", t.lateBets), t.lateBets)
		}
	case EarlyBird.ID:
		if !p.IsAI && t.earlyBets >= minTimingPicks {
			return yes(fmt.Sprintf("%d picks placed 48h+ ahead", t.earlyBets), t.earlyBets)
		}
	case SecondGuesser.ID:
		if !p.IsAI && t.edits >= minEdits {
			return yes(plural(t.edits, "edited pick"), t.edits)
		}
	case Contrarian.ID:
		if t.contrarianPts > 0 {
			return yes(fmt.Sprintf("%d pts from rare picks", t.contrarianPts), t.contrarianPts)
		}
	case PerfectRound.ID:
		if len(t.perfectRounds) > 0 {
			return yes("round of "+t.perfectRounds[0], len(t.perfectRounds))
		}
	case Blackout.ID:
		if len(t.blackoutRounds) > 0 {
			return yes("round of "+t.blackoutRounds[0], len(t.blackoutRounds))
		}
	case HotHand.ID:
		if t.bestExactStreak >= minHotHand {
			return yes(fmt.Sprintf("%d exact scores in a row", t.bestExactStreak), t.bestExactStreak)
		}
	case EverPresent.ID:
		if p.Part.Available >= everPresentMin && p.Part.Bet == p.Part.Available {
			return yes(fmt.Sprintf("all %d matches picked", p.Part.Available), p.Part.Available)
		}
	case Quitter.ID:
		if late && p.Part.Bet > 0 && p.Part.RecentSkips >= quitterMinTail {
			return yes(fmt.Sprintf("last %d games unpicked", p.Part.RecentSkips), p.Part.RecentSkips)
		}
	case WireToWire.ID:
		if t.ledAll && len(p.Rounds) >= wireMinRounds {
			return yes(fmt.Sprintf("led all %d rounds", len(p.Rounds)), len(p.Rounds))
		}
	}
	return no()
}

// timingValid reports whether a pick's timestamps mean anything: it must have
// been placed before kickoff. A created_at at/after kickoff is an ai-seed or
// place-bet escape-hatch insert — never "late", never "early".
func timingValid(pk Pick) bool {
	return !pk.PlacedAt.IsZero() && pk.PlacedAt.Before(pk.Kickoff)
}

// avgGoals renders the average predicted goals with one decimal, e.g. "3.4
// goals per pick" — shared by Goal Merchant and The Accountant.
func avgGoals(t tally) string {
	return fmt.Sprintf("%.1f goals per pick", float64(t.goalsTotal)/float64(t.betCount))
}

// plural formats "1 exact score" / "4 exact scores". An irregular plural can be
// passed explicitly as the third argument.
func plural(n int, singular string, pluralForm ...string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	if len(pluralForm) > 0 {
		return fmt.Sprintf("%d %s", n, pluralForm[0])
	}
	return fmt.Sprintf("%d %ss", n, singular)
}
