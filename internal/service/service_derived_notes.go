package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"bethoven/internal/ai"
	"bethoven/internal/models"
)

// settingDerivedNotes is the KV key for BETanIA's auto-derived "house notes": a
// JSON diary with ONE entry per finished match (its story), plus the set of match
// ids already noted. A SEPARATE tier from the admin's comment_context.Notes.
const settingDerivedNotes = "comment_derived_notes"

// derivedPendingCap bounds how many per-match notes one pass will generate, a
// backstop against a burst if several matches finished between passes (normally
// 0-1 new per finish). Excess matches are picked up on the next pass.
const derivedPendingCap = 4

// derivedNoteFeedCap bounds how many stored notes are fed to the comment prompt at
// once, so the running diary can't balloon the prompt; the admin compacts the rest.
const derivedNoteFeedCap = 8

// derivedLiveStoryCap bounds how many live-commentary lines are pulled into one
// match's digest as its "story".
const derivedLiveStoryCap = 30

// derivedSnapshotCap bounds how many leaderboard "dance" frames are pulled into one
// match's digest. A frame is logged per real standings shift (≈ per goal), so this
// comfortably covers a whole match while keeping the digest prompt bounded.
const derivedSnapshotCap = 40

type derivedNote struct {
	MatchID int64  `json:"match_id,omitempty"`
	Text    string `json:"text"`
	At      string `json:"at"` // RFC3339
}

// storedDerived is the diary: one note per finished match, plus Done (the match ids
// already noted) so each game is summarized exactly once. Seeded marks that the
// first encounter has adopted the already-finished matches as done — so enabling
// the feature mid-tournament doesn't backfill every past game, only narrates games
// finishing from here on.
type storedDerived struct {
	Seeded bool          `json:"seeded"`
	Done   []int64       `json:"done"`
	Notes  []derivedNote `json:"notes"`
}

func (d storedDerived) isDone(id int64) bool {
	for _, x := range d.Done {
		if x == id {
			return true
		}
	}
	return false
}

func (s *Service) loadDerivedNotes() storedDerived {
	var d storedDerived
	if v, err := s.store.GetSetting(settingDerivedNotes); err == nil {
		_ = json.Unmarshal([]byte(v), &d)
	}
	return d
}

func (s *Service) saveDerivedNotes(d storedDerived) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return s.store.SetSetting(settingDerivedNotes, string(b))
}

// SetAICommentLogPath tells the service where the comment log lives, so it can
// recover BETanIA's live-commentary "story" of a finished match for the digest.
// Optional — unset ⇒ the digest just omits the live story.
func (s *Service) SetAICommentLogPath(p string) { s.aiCommentLogPath = p }

// --- worker seams (not admin-gated: the worker is the caller) ---

// PendingDigests returns one digest input per finished match that doesn't have a
// derived note yet (oldest first, capped) — so the worker writes ONE note per game.
// On the very first encounter it adopts all already-finished matches as "done"
// without generating anything (no backfill), then narrates only games that finish
// from then on. Worker seam.
func (s *Service) PendingDigests() ([]ai.ResultsDigestData, error) {
	matches, err := s.store.ListMatches(s.tournamentID)
	if err != nil {
		return nil, err
	}
	finished := make([]models.Match, 0, len(matches))
	for _, m := range matches {
		if m.Finished && m.ScoreA != nil && m.ScoreB != nil {
			finished = append(finished, m)
		}
	}
	// Chronological, so the diary appends in the order games actually ended.
	sort.Slice(finished, func(i, j int) bool { return finished[i].StartsAt.Before(finished[j].StartsAt) })

	d := s.loadDerivedNotes()
	if !d.Seeded {
		// First run: adopt the current slate as already-noted, generate nothing.
		d.Seeded = true
		d.Done = d.Done[:0]
		for _, m := range finished {
			d.Done = append(d.Done, m.ID)
		}
		if err := s.saveDerivedNotes(d); err != nil {
			return nil, err
		}
		return nil, nil
	}

	sc, err := s.newScorer()
	if err != nil {
		return nil, err
	}
	var out []ai.ResultsDigestData
	for _, m := range finished {
		if d.isDone(m.ID) {
			continue
		}
		out = append(out, s.matchDigestData(m, sc))
		if len(out) >= derivedPendingCap {
			break
		}
	}
	return out, nil
}

// matchDigestData builds the digest input for ONE finished match: its result, every
// player's pick + points, and the live-commentary "story" logged while it was on.
func (s *Service) matchDigestData(m models.Match, sc scorer) ai.ResultsDigestData {
	fm := ai.FinishedMatchDigest{
		TeamA: m.TeamA,
		TeamB: m.TeamB,
		Score: fmt.Sprintf("%d-%d", *m.ScoreA, *m.ScoreB),
		Stage: stageLabel(m),
	}
	if bets, err := s.store.BetsForMatch(m.ID); err == nil && len(bets) > 0 {
		ids := make([]int64, 0, len(bets))
		for _, b := range bets {
			ids = append(ids, b.UserID)
		}
		users, _ := s.store.UsersByIDs(ids)
		for _, b := range bets {
			fm.Picks = append(fm.Picks, ai.DigestPick{
				Player: users[b.UserID].DisplayName,
				Pred:   fmt.Sprintf("%d-%d", b.PredA, b.PredB),
				Points: sc.points(b, m),
			})
		}
	}
	data := ai.ResultsDigestData{MatchID: m.ID, Matches: []ai.FinishedMatchDigest{fm}}
	// The live "story" of THIS game: lines logged from its kickoff onward, plus the
	// leaderboard "dance" frames captured as the goals went in — so the note can recount
	// both the play-by-play and how the pool table moved during the match.
	if s.aiCommentLogPath != "" {
		data.LiveStory = ai.RecentLiveComments(s.aiCommentLogPath, m.StartsAt, derivedLiveStoryCap)
		data.LiveSnapshots = ai.RecentLiveSnapshots(s.aiCommentLogPath, m.StartsAt, derivedSnapshotCap)
	}
	return data
}

// stageLabel renders a short stage/round label for the digest.
func stageLabel(m models.Match) string {
	if m.GroupLabel != "" {
		return m.GroupLabel
	}
	return string(m.Phase)
}

// noteDateLabel renders a derived note's calendar date (parsed from its RFC3339 At)
// for the prompt, e.g. "Jun 22". Empty when At is missing/unparseable — older notes
// still feed fine, just without a tag.
func noteDateLabel(at string) string {
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return ""
	}
	return t.Format("Jan 2")
}

// datedNote prefixes a note's text with the date it was recorded (≈ when the match
// was played), so BETanIA can tell a fresh result from a days-old one instead of
// describing every finished game as if it just happened.
func datedNote(n derivedNote) string {
	if d := noteDateLabel(n.At); d != "" {
		return "[" + d + "] " + n.Text
	}
	return n.Text
}

// DerivedNotesText returns the combined derived-notes text to feed the comment
// prompt (the last derivedNoteFeedCap game stories, oldest first). Each entry is
// tagged with the date it was played and the block is led by today's date, so the
// model anchors recency correctly and never frames an old game as if it just
// happened. Worker seam.
func (s *Service) DerivedNotesText() string {
	d := s.loadDerivedNotes()
	notes := d.Notes
	if len(notes) > derivedNoteFeedCap {
		notes = notes[len(notes)-derivedNoteFeedCap:]
	}
	if len(notes) == 0 {
		return ""
	}
	lines := make([]string, 0, len(notes)+1)
	lines = append(lines, "Today is "+s.Now().Format("Jan 2")+".")
	for _, n := range notes {
		lines = append(lines, datedNote(n))
	}
	return strings.Join(lines, "\n")
}

// AddDerivedNote appends one finished match's story and marks that match done, so
// it's narrated exactly once. Worker seam. The text is already sanitized by the
// worker. A zero matchID (defensive) still stores the note but can't dedupe.
func (s *Service) AddDerivedNote(matchID int64, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	d := s.loadDerivedNotes()
	d.Notes = append(d.Notes, derivedNote{MatchID: matchID, Text: text, At: s.Now().UTC().Format(time.RFC3339)})
	if matchID != 0 && !d.isDone(matchID) {
		d.Done = append(d.Done, matchID)
	}
	return s.saveDerivedNotes(d)
}

// --- admin curation ---

// DerivedNoteView is one derived note for the admin context screen.
type DerivedNoteView struct {
	Text string
	At   time.Time
}

// DerivedNotes returns BETanIA's auto-derived house-notes snapshots (oldest
// first) for the admin context screen. Admin only.
func (s *Service) DerivedNotes(by *models.User) ([]DerivedNoteView, error) {
	if err := requireAdmin(by); err != nil {
		return nil, err
	}
	d := s.loadDerivedNotes()
	out := make([]DerivedNoteView, 0, len(d.Notes))
	for _, n := range d.Notes {
		at, _ := time.Parse(time.RFC3339, n.At)
		out = append(out, DerivedNoteView{Text: n.Text, At: at})
	}
	return out, nil
}

// DeleteDerivedNote removes the derived note at idx (as ordered by DerivedNotes).
// Admin only.
func (s *Service) DeleteDerivedNote(by *models.User, idx int) error {
	if err := requireAdmin(by); err != nil {
		return err
	}
	d := s.loadDerivedNotes()
	if idx < 0 || idx >= len(d.Notes) {
		return errors.New("no such note")
	}
	d.Notes = append(d.Notes[:idx], d.Notes[idx+1:]...)
	return s.saveDerivedNotes(d)
}

// ClearDerivedNotes wipes the diary entirely. It resets Seeded/Done too, so the
// next pass re-adopts the current finished slate as "done" (no backfill) and only
// narrates games finishing afterwards — clearing never triggers a regeneration
// burst over past games. Admin only.
func (s *Service) ClearDerivedNotes(by *models.User) error {
	if err := requireAdmin(by); err != nil {
		return err
	}
	return s.saveDerivedNotes(storedDerived{})
}

// compactNotesTimeout bounds the single model call that fuses the diary.
const compactNotesTimeout = 3 * time.Minute

// CompactDerivedNotes fuses the whole per-game diary into ONE consolidated note via
// BETanIA — a single narrative of the pool's dynamics that weights recent games most
// (not a flat list, and not just the latest entry). Done is kept intact, so compacting
// never causes past games to be re-narrated. The synthesized note carries no match id
// (it spans several), so it never collides with the per-game dedupe. Synchronous (one
// model call) — callers should run it off the UI thread. Admin only.
//
// Falls back to trimming the diary to its latest entry when the comment worker isn't
// attached (nil compactor). On a model error the diary is left untouched and the error
// is returned, so a transient failure never destroys the backlog.
func (s *Service) CompactDerivedNotes(by *models.User) error {
	if err := requireAdmin(by); err != nil {
		return err
	}
	d := s.loadDerivedNotes()
	if len(d.Notes) <= 1 {
		return nil
	}
	if s.aiNotesCompactor == nil {
		// No worker: degrade to the old trim-to-latest behaviour.
		d.Notes = d.Notes[len(d.Notes)-1:]
		return s.saveDerivedNotes(d)
	}

	// Date-tag each entry so the fused narrative keeps the time references — without
	// them BETanIA collapses the whole diary into "today" and retells old games as
	// fresh. The compaction prompt is told to preserve these dates.
	texts := make([]string, 0, len(d.Notes))
	for _, n := range d.Notes {
		texts = append(texts, datedNote(n))
	}
	ctx, cancel := context.WithTimeout(context.Background(), compactNotesTimeout)
	defer cancel()
	summary, err := s.aiNotesCompactor(ctx, texts)
	if err != nil {
		return err
	}
	if strings.TrimSpace(summary) == "" {
		return errors.New("compaction produced no text")
	}
	d.Notes = []derivedNote{{Text: summary, At: s.Now().UTC().Format(time.RFC3339)}}
	return s.saveDerivedNotes(d)
}
