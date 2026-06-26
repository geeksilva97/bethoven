package ai

// CardPick is one notable pick on a player's end-of-tournament card — their best
// call or worst miss — grounding the narrative in a concrete moment.
type CardPick struct {
	Match  string `json:"match"` // "Spain vs Germany"
	Stage  string `json:"stage,omitempty"`
	Pred   string `json:"pred"`   // the player's prediction, "2-1"
	Actual string `json:"actual"` // the real regulation-90' result, "2-1"
	Points int    `json:"points"`
}

// CardRound is one round of a player's personal trajectory through the tournament:
// where they sat, with how many points, and how they moved that round.
type CardRound struct {
	Round        string `json:"round"`
	Position     int    `json:"position"`
	Total        int    `json:"total"`
	Movement     int    `json:"movement"` // + climbed, − fell, 0 at round 1
	PointsGained int    `json:"points_gained"`
}

// CardDigestData is the grounding for ONE player's end-of-tournament card
// narrative — everything BETanIA needs to tell their hero's journey without
// inventing anything. Built by the service (which never lets the ai package import
// it); the model sees it as untrusted JSON data. IsSelf flips BETanIA's own card to
// first person. Story is the combined derived-notes "story of the tournament".
type CardDigestData struct {
	UserID         int64       `json:"-"`
	Player         string      `json:"player"`
	IsSelf         bool        `json:"is_self"`
	FinalRank      int         `json:"final_rank"`
	TotalPlayers   int         `json:"total_players"`
	TotalPoints    int         `json:"total_points"`
	ExactHits      int         `json:"exact_hits"`
	CorrectResults int         `json:"correct_results"`
	StartRank      int         `json:"start_rank"`
	PeakRank       int         `json:"peak_rank"`
	Trajectory     []CardRound `json:"trajectory"`
	BestPick       *CardPick   `json:"best_pick,omitempty"`
	WorstPick      *CardPick   `json:"worst_pick,omitempty"`
	Story          string      `json:"story,omitempty"`
}
