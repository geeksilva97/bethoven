package service

import (
	"errors"
	"time"

	"bethoven/internal/ai"
	"bethoven/internal/db"
	"bethoven/internal/models"
)

// settingCommentTone is the KV key for BETanIA's leaderboard-comment tone. Stored
// as "playful" (default) or "savage"; absent means playful. Read/written like
// scoring_mode / public_bets, so existing pools are unaffected until an admin opts in.
const settingCommentTone = "comment_tone"

// CommentTone reports the active comment tone, defaulting to "playful".
func (s *Service) CommentTone() (string, error) {
	v, err := s.store.GetSetting(settingCommentTone)
	if errors.Is(err, db.ErrNotFound) {
		return "playful", nil
	}
	if err != nil {
		return "playful", err
	}
	if v != "savage" {
		return "playful", nil
	}
	return v, nil
}

// SetCommentTone changes BETanIA's comment tone ("playful" or "savage"). Admin only.
func (s *Service) SetCommentTone(by *models.User, tone string) error {
	if err := requireAdmin(by); err != nil {
		return err
	}
	if tone != "savage" {
		tone = "playful"
	}
	if err := s.store.SetSetting(settingCommentTone, tone); err != nil {
		return err
	}
	s.track(by, by.Fingerprint, EvSettingChanged, map[string]string{
		"setting": settingCommentTone,
		"value":   tone,
	})
	return nil
}

// CommentSource exposes BETanIA's current per-player comments (the in-memory
// cache). nil is valid and means the comment worker isn't running, in which case
// the leaderboard shows no comments. Mirrors the LiveStore / AIMonitor ports.
type CommentSource interface {
	All(now time.Time) map[int64]ai.Comment
}

// SetCommentSource attaches the comment cache. Optional — unset ⇒ no comments.
func (s *Service) SetCommentSource(src CommentSource) { s.comments = src }

// LeaderboardComments returns the BETanIA comments to show on the leaderboard,
// keyed by user id. Scoped server-side: a player sees ONLY their own comment; an
// admin sees everyone's. The scoping is the visibility boundary (mirroring AllBets
// vs own-bets), enforced here rather than just hidden in the UI.
func (s *Service) LeaderboardComments(by *models.User) map[int64]string {
	if s.comments == nil || by == nil {
		return nil
	}
	all := s.comments.All(s.Now())
	out := make(map[int64]string, len(all))
	if by.Role == models.RoleAdmin {
		for id, c := range all {
			// Mute is enforced at READ time too: if an admin mutes a player, their
			// already-cached comment stops showing immediately, without waiting for
			// the next regeneration pass.
			if s.userToneOverride(id) == "mute" {
				continue
			}
			out[id] = c.Text
		}
		return out
	}
	if s.userToneOverride(by.ID) == "mute" {
		return out
	}
	if c, ok := all[by.ID]; ok {
		out[by.ID] = c.Text
	}
	return out
}

// AICommentMonitor is the optional observability port for the comment worker. The
// concrete implementation (ai.CommentMonitor) is written by the worker and read
// here for the admin panel. nil ⇒ "not running" ⇒ reads return ErrAIOff.
type AICommentMonitor interface {
	Status() ai.CommentStatus
	Activity(limit int) []ai.CommentAction
}

// SetCommentMonitor attaches the comment worker's monitor. Optional.
func (s *Service) SetCommentMonitor(m AICommentMonitor) { s.aiComments = m }

// SetCommentTrigger attaches the comment worker's "regenerate all" hook
// (CommentWorker.Trigger). Optional. The func returns false when a pass is queued.
func (s *Service) SetCommentTrigger(fn func() bool) { s.aiCommentTrigger = fn }

// AICommentStatus returns the comment worker's status. Admin only.
func (s *Service) AICommentStatus(by *models.User) (ai.CommentStatus, error) {
	if err := requireAdmin(by); err != nil {
		return ai.CommentStatus{}, err
	}
	if s.aiComments == nil {
		return ai.CommentStatus{}, ErrAIOff
	}
	return s.aiComments.Status(), nil
}

// AICommentActivity returns the worker's most recent comments, newest first. Admin only.
func (s *Service) AICommentActivity(by *models.User, limit int) ([]ai.CommentAction, error) {
	if err := requireAdmin(by); err != nil {
		return nil, err
	}
	if s.aiComments == nil {
		return nil, ErrAIOff
	}
	return s.aiComments.Activity(limit), nil
}

// TriggerAIComments asks the comment worker to regenerate ALL players' comments
// now (the admin "regenerate all" control). Admin only. Returns ErrAIOff when no
// worker is attached and ErrAIBusy when a pass is already queued.
func (s *Service) TriggerAIComments(by *models.User) error {
	if err := requireAdmin(by); err != nil {
		return err
	}
	if s.aiCommentTrigger == nil {
		return ErrAIOff
	}
	if !s.aiCommentTrigger() {
		return ErrAIBusy
	}
	return nil
}
