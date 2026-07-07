package ai

// Quitters (defectors — players who were picking and then abandoned the pool
// down the stretch) get a FIXED, pre-generated dismissal instead of a
// model-written roast: BETanIA disapproves of desertion so thoroughly she won't
// spend tokens on it — and the line says so. The per-player comment writer is
// told not to write for them (and the worker drops any line that slips through);
// the worker swaps in one of these at zero model cost. The live commentary and
// the derived-note/rivalry/card tiers keep their model-written desertion angles —
// this is only the per-player leaderboard line.
//
// Same output boundary as every comment: these render into terminals, so keep
// them one sentence, ~140 chars, no emojis, no line breaks — and they must stay
// clean text (they skip sanitizeText, being compile-time constants).
var quitterComments = []string{
	"You quit on the pool, so the pool quits on you: this is a form letter, because you stopped being worth the tokens.",
	"You walked out mid-tournament, so you get the pre-printed note. Deserters don't earn compute.",
	"This line wasn't generated for you — quitters get the template. Start picking again and I'll start thinking about you again.",
	"You bailed on the run-in, so I won't spend a single token on you. This is a photocopy. It says: shame.",
	"Everyone still playing gets bespoke banter; you get boilerplate. That's what ghosting the pool buys.",
	"I roast players, not empty chairs. This is a fixed message for a fixed absence: quitting is a bad look.",
}

// QuitterComment picks a quitter's fixed line, keyed by user id so each deserter
// keeps a stable message across passes (deterministic — no clock, no RNG).
func QuitterComment(userID int64) string {
	if userID < 0 {
		userID = -userID
	}
	return quitterComments[int(userID%int64(len(quitterComments)))]
}

// defectorSet returns the display names flagged as defectors, for the worker's
// drop-and-replace. The set-shaped sibling of defectorNames.
func defectorSet(parts []PlayerParticipation) map[string]bool {
	set := make(map[string]bool)
	for _, p := range parts {
		if p.Defector {
			set[p.Name] = true
		}
	}
	return set
}
