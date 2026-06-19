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

// LeaderboardComments returns the BETanIA comment to show on the leaderboard,
// keyed by user id. Scoped server-side: EVERYONE — players and admins alike — sees
// ONLY their own comment. Admins review the full set in the BETanIA admin panel
// (and can cycle through them on the leaderboard via AllLeaderboardComments), so
// the leaderboard itself stays uncluttered. Enforced here, not just hidden in the UI.
func (s *Service) LeaderboardComments(by *models.User) map[int64]string {
	if s.comments == nil || by == nil {
		return nil
	}
	all := s.comments.All(s.Now())
	out := make(map[int64]string, 1)
	if s.userToneOverride(by.ID) == "mute" {
		return out
	}
	if c, ok := all[by.ID]; ok {
		out[by.ID] = c.Text
	}
	return out
}

// AllLeaderboardComments returns every player's BETanIA comment, keyed by user id.
// Open to all — comments are a shared, fun feature: any player can cycle through
// everyone's takes on the (public) standings. (Bets stay private; that's a separate
// boundary.) Mute is honored at READ time, so a muted player's comment is excluded
// for everyone immediately, never waiting for the next regeneration pass.
func (s *Service) AllLeaderboardComments() map[int64]string {
	if s.comments == nil {
		return map[int64]string{}
	}
	all := s.comments.All(s.Now())
	out := make(map[int64]string, len(all))
	for id, c := range all {
		if s.userToneOverride(id) == "mute" {
			continue
		}
		out[id] = c.Text
	}
	return out
}

// IsMuted reports whether a user's per-player comment tone is "mute". Used by the
// TUI to keep muted players out of the comment cycle entirely (they get no comment
// of their own and don't see others' rotate by). Not gated — it's about the caller.
func (s *Service) IsMuted(u *models.User) bool {
	return u != nil && s.userToneOverride(u.ID) == "mute"
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

// AICommentActivity returns the worker's most recent comments, newest first. Admin
// only. Muted players are dropped at READ time: the worker stops recording them once
// muted, but pre-mute entries linger in the in-memory ring until they age out — so a
// muted player must show NO comment anywhere, the admin feed included. Error entries
// (no player) are always kept.
func (s *Service) AICommentActivity(by *models.User, limit int) ([]ai.CommentAction, error) {
	if err := requireAdmin(by); err != nil {
		return nil, err
	}
	if s.aiComments == nil {
		return nil, ErrAIOff
	}
	muted := s.mutedNames()
	all := s.aiComments.Activity(0) // fetch all, then filter, then cap
	out := make([]ai.CommentAction, 0, len(all))
	for _, a := range all {
		if a.Player != "" && muted[a.Player] {
			continue
		}
		out = append(out, a)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// mutedNames returns the set of display names whose per-player tone is "mute".
func (s *Service) mutedNames() map[string]bool {
	users, err := s.store.AllUsers()
	if err != nil {
		return nil
	}
	out := make(map[string]bool)
	for _, u := range users {
		if s.userToneOverride(u.ID) == "mute" {
			out[u.DisplayName] = true
		}
	}
	return out
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
