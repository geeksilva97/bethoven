package service

import (
	"errors"

	"bethoven/internal/db"
	"bethoven/internal/models"
	"bethoven/internal/scoring"
)

// settingScoringMode is the KV key for the active scoring rule. Stored as the
// Mode's String() value ("classic"/"proximity"/"scarcity"); absent means classic.
const settingScoringMode = "scoring_mode"

// ScoringMode reports the active scoring mode. Defaults to Classic when the
// setting has never been written (so existing pools are unchanged).
func (s *Service) ScoringMode() (scoring.Mode, error) {
	v, err := s.store.GetSetting(settingScoringMode)
	if errors.Is(err, db.ErrNotFound) {
		return scoring.ModeClassic, nil
	}
	if err != nil {
		return scoring.ModeClassic, err
	}
	return scoring.ParseMode(v), nil
}

// SetScoringMode changes the active scoring mode. Admin only.
func (s *Service) SetScoringMode(by *models.User, mode scoring.Mode) error {
	if err := requireAdmin(by); err != nil {
		return err
	}
	if err := s.store.SetSetting(settingScoringMode, mode.String()); err != nil {
		return err
	}
	s.track(by, by.Fingerprint, EvSettingChanged, map[string]string{
		"setting": settingScoringMode,
		"value":   mode.String(),
	})
	return nil
}

// settingRoundWeights is the KV key for the round-weight scheme that scales a
// match's points by its tournament phase. Stored as the scheme's String()
// ("flat"/"doubling"/"linear"); absent means flat (every match ×1).
const settingRoundWeights = "round_weights"

// RoundWeights reports the active round-weight scheme. Defaults to Flat (all
// matches equal) when the setting has never been written, so existing pools are
// unchanged until an admin opts in.
func (s *Service) RoundWeights() (scoring.WeightScheme, error) {
	v, err := s.store.GetSetting(settingRoundWeights)
	if errors.Is(err, db.ErrNotFound) {
		return scoring.WeightFlat, nil
	}
	if err != nil {
		return scoring.WeightFlat, err
	}
	return scoring.ParseWeightScheme(v), nil
}

// SetRoundWeights changes the active round-weight scheme. Admin only.
func (s *Service) SetRoundWeights(by *models.User, w scoring.WeightScheme) error {
	if err := requireAdmin(by); err != nil {
		return err
	}
	if err := s.store.SetSetting(settingRoundWeights, w.String()); err != nil {
		return err
	}
	s.track(by, by.Fingerprint, EvSettingChanged, map[string]string{
		"setting": settingRoundWeights,
		"value":   w.String(),
	})
	return nil
}

// scorer scores bets under the active mode. For Scarcity it precomputes each
// match's pick distribution once (from all bets in the tournament); for Classic
// and Proximity it carries no pool data and the per-bet call is allocation-free.
type scorer struct {
	mode    scoring.Mode
	weights scoring.WeightScheme     // per-phase point multiplier (flat ⇒ all ×1)
	total   map[int64]int            // matchID -> #bets
	result  map[int64]map[int]int    // matchID -> W/D/L sign -> #bets
	exact   map[int64]map[[2]int]int // matchID -> [predA,predB] -> #bets
}

// newScorer reads the active mode and, only for Scarcity, loads the per-match
// pick distribution it needs. Cheap (one setting read) for the other modes.
func (s *Service) newScorer() (scorer, error) {
	mode, err := s.ScoringMode()
	if err != nil {
		return scorer{}, err
	}
	weights, err := s.RoundWeights()
	if err != nil {
		return scorer{}, err
	}
	sc := scorer{mode: mode, weights: weights}
	if mode == scoring.ModeScarcity {
		bets, err := s.store.AllBets(s.tournamentID)
		if err != nil {
			return scorer{}, err
		}
		sc.buildPools(bets)
	}
	return sc, nil
}

// buildPools tallies, per match, the total bets and how many share each W/D/L
// result and each exact scoreline.
func (sc *scorer) buildPools(bets []models.Bet) {
	sc.total = make(map[int64]int)
	sc.result = make(map[int64]map[int]int)
	sc.exact = make(map[int64]map[[2]int]int)
	for _, b := range bets {
		sc.total[b.MatchID]++
		if sc.result[b.MatchID] == nil {
			sc.result[b.MatchID] = make(map[int]int)
			sc.exact[b.MatchID] = make(map[[2]int]int)
		}
		sc.result[b.MatchID][scoring.Result(b)]++
		sc.exact[b.MatchID][[2]int{b.PredA, b.PredB}]++
	}
}

// points scores one bet against one match under the active mode, then scales by
// the match's round weight. For Scarcity it looks up the match's pick
// distribution; other modes ignore the pool.
func (sc scorer) points(b models.Bet, m models.Match) int {
	return scoring.Score(sc.mode, b, m, sc.pool(b, m)) * sc.weights.Weight(m.Phase)
}

// explain returns the human-readable points breakdown for one bet, using the
// same pool the scorer would score with and applying the same round weight — so
// the explanation always matches the points shown.
func (sc scorer) explain(b models.Bet, m models.Match) scoring.Breakdown {
	bd := scoring.Explain(sc.mode, b, m, sc.pool(b, m))
	return bd.ApplyWeight(sc.weights.Weight(m.Phase), m.Phase.Label())
}

// pool returns the per-match pick distribution for Scarcity, or the zero Pool
// for modes that ignore it.
func (sc scorer) pool(b models.Bet, m models.Match) scoring.Pool {
	if sc.mode != scoring.ModeScarcity {
		return scoring.Pool{}
	}
	return scoring.Pool{
		Total:      sc.total[m.ID],
		SameResult: sc.result[m.ID][scoring.Result(b)],
		SameExact:  sc.exact[m.ID][[2]int{b.PredA, b.PredB}],
	}
}
