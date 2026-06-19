package service

import (
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
// JSON list of snapshot entries plus the results signature the latest one was
// built from. A SEPARATE tier from the admin's comment_context.Notes — never mixed.
const settingDerivedNotes = "comment_derived_notes"

// derivedDigestMatchCap bounds how many recently finished matches feed one digest.
const derivedDigestMatchCap = 6

// derivedNoteFeedCap bounds how many stored notes are fed to the comment prompt at
// once, so the running diary can't balloon the prompt; the admin compacts the rest.
const derivedNoteFeedCap = 8

// derivedLiveStoryCap bounds how many live-commentary lines are pulled into one
// digest as the game's "story".
const derivedLiveStoryCap = 30

type derivedNote struct {
	Text string `json:"text"`
	At   string `json:"at"` // RFC3339
}

type storedDerived struct {
	Sig   string        `json:"sig"`
	Notes []derivedNote `json:"notes"`
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

// ResultsDigestData builds the input for BETanIA's derived-notes snapshot: the
// most recently finished matches (newest first), every player's pick + points on
// them, and the live-commentary lines that played while those games were on.
func (s *Service) ResultsDigestData() (ai.ResultsDigestData, error) {
	matches, err := s.store.ListMatches(s.tournamentID)
	if err != nil {
		return ai.ResultsDigestData{}, err
	}
	finished := make([]models.Match, 0, len(matches))
	for _, m := range matches {
		if m.Finished && m.ScoreA != nil && m.ScoreB != nil {
			finished = append(finished, m)
		}
	}
	if len(finished) == 0 {
		return ai.ResultsDigestData{}, nil
	}
	// Newest first, capped.
	sort.Slice(finished, func(i, j int) bool { return finished[i].StartsAt.After(finished[j].StartsAt) })
	if len(finished) > derivedDigestMatchCap {
		finished = finished[:derivedDigestMatchCap]
	}

	sc, err := s.newScorer()
	if err != nil {
		return ai.ResultsDigestData{}, err
	}

	out := ai.ResultsDigestData{}
	earliest := finished[0].StartsAt
	for _, m := range finished {
		if m.StartsAt.Before(earliest) {
			earliest = m.StartsAt
		}
		fm := ai.FinishedMatchDigest{
			TeamA: m.TeamA,
			TeamB: m.TeamB,
			Score: fmt.Sprintf("%d-%d", *m.ScoreA, *m.ScoreB),
			Stage: stageLabel(m),
		}
		bets, err := s.store.BetsForMatch(m.ID)
		if err != nil {
			return ai.ResultsDigestData{}, err
		}
		if len(bets) > 0 {
			ids := make([]int64, 0, len(bets))
			for _, b := range bets {
				ids = append(ids, b.UserID)
			}
			users, err := s.store.UsersByIDs(ids)
			if err != nil {
				return ai.ResultsDigestData{}, err
			}
			for _, b := range bets {
				fm.Picks = append(fm.Picks, ai.DigestPick{
					Player: users[b.UserID].DisplayName,
					Pred:   fmt.Sprintf("%d-%d", b.PredA, b.PredB),
					Points: sc.points(b, m),
				})
			}
		}
		out.Matches = append(out.Matches, fm)
	}

	// The live "story": commentary lines logged while these games were on.
	if s.aiCommentLogPath != "" {
		out.LiveStory = ai.RecentLiveComments(s.aiCommentLogPath, earliest, derivedLiveStoryCap)
	}
	return out, nil
}

// stageLabel renders a short stage/round label for the digest.
func stageLabel(m models.Match) string {
	if m.GroupLabel != "" {
		return m.GroupLabel
	}
	return string(m.Phase)
}

// DerivedNotesText returns the combined derived-notes text to feed the comment
// prompt (the last derivedNoteFeedCap notes, oldest first) plus the results
// signature the latest note was built from. Worker seam.
func (s *Service) DerivedNotesText() (string, string) {
	d := s.loadDerivedNotes()
	notes := d.Notes
	if len(notes) > derivedNoteFeedCap {
		notes = notes[len(notes)-derivedNoteFeedCap:]
	}
	texts := make([]string, 0, len(notes))
	for _, n := range notes {
		texts = append(texts, n.Text)
	}
	return strings.Join(texts, "\n"), d.Sig
}

// AddDerivedNote appends a freshly generated snapshot + the signature it was built
// from. Worker seam. The text is already sanitized by the worker.
func (s *Service) AddDerivedNote(text, sig string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	d := s.loadDerivedNotes()
	d.Notes = append(d.Notes, derivedNote{Text: text, At: s.Now().UTC().Format(time.RFC3339)})
	d.Sig = sig
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

// ClearDerivedNotes drops all derived notes (the next finish regenerates one).
// The signature is reset too, so the next pass always rebuilds. Admin only.
func (s *Service) ClearDerivedNotes(by *models.User) error {
	if err := requireAdmin(by); err != nil {
		return err
	}
	return s.saveDerivedNotes(storedDerived{})
}

// CompactDerivedNotes collapses the running diary down to just the most recent
// snapshot, keeping the freshest "story" while trimming the backlog the prompt
// carries. Admin only. (The signature is preserved so a compact doesn't force a
// needless regeneration on the next pass.)
func (s *Service) CompactDerivedNotes(by *models.User) error {
	if err := requireAdmin(by); err != nil {
		return err
	}
	d := s.loadDerivedNotes()
	if len(d.Notes) <= 1 {
		return nil
	}
	d.Notes = d.Notes[len(d.Notes)-1:]
	return s.saveDerivedNotes(d)
}
