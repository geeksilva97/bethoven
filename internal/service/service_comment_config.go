package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"bethoven/internal/ai"
	"bethoven/internal/models"
)

// settingUserTonePrefix keys a per-player tone override, e.g.
// "comment_tone_u_42" = "savage". Absent (or "default") ⇒ inherit the global tone.
const settingUserTonePrefix = "comment_tone_u_"

// userToneOverride returns a player's stored override ("playful"/"savage"/"mute")
// or "" when none is set (inherit the default).
func (s *Service) userToneOverride(userID int64) string {
	v, err := s.store.GetSetting(fmt.Sprintf("%s%d", settingUserTonePrefix, userID))
	if err != nil {
		return ""
	}
	switch v {
	case "playful", "savage", "mute":
		return v
	default:
		return ""
	}
}

// SetUserCommentTone sets a player's tone override. tone is one of
// "default"|"playful"|"savage"|"mute" ("default" clears the override). Admin only.
func (s *Service) SetUserCommentTone(by *models.User, userID int64, tone string) error {
	if err := requireAdmin(by); err != nil {
		return err
	}
	switch tone {
	case "default", "playful", "savage", "mute":
	default:
		return fmt.Errorf("invalid tone %q", tone)
	}
	return s.store.SetSetting(fmt.Sprintf("%s%d", settingUserTonePrefix, userID), tone)
}

// PlayerTone pairs a player with their effective per-user tone setting.
type PlayerTone struct {
	User models.User
	Tone string // "default"|"playful"|"savage"|"mute"
}

// PlayerTones lists every player with their per-user tone, for the admin editor. Admin only.
func (s *Service) PlayerTones(by *models.User) ([]PlayerTone, error) {
	if err := requireAdmin(by); err != nil {
		return nil, err
	}
	users, err := s.store.AllUsers()
	if err != nil {
		return nil, err
	}
	out := make([]PlayerTone, 0, len(users))
	for _, u := range users {
		t := s.userToneOverride(u.ID)
		if t == "" {
			t = "default"
		}
		out = append(out, PlayerTone{User: u, Tone: t})
	}
	return out, nil
}

// settingCommentContext is the KV key for admin-entered comment context (a JSON
// blob of rivalries + house notes).
const settingCommentContext = "comment_context"

type storedRivalry struct {
	A    int64  `json:"a"`
	B    int64  `json:"b"`
	Note string `json:"note"`
}

type storedContext struct {
	Rivalries []storedRivalry `json:"rivalries"`
	Notes     []string        `json:"notes"`
}

func (s *Service) loadStoredContext() storedContext {
	var c storedContext
	if v, err := s.store.GetSetting(settingCommentContext); err == nil {
		_ = json.Unmarshal([]byte(v), &c)
	}
	return c
}

func (s *Service) saveStoredContext(c storedContext) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.store.SetSetting(settingCommentContext, string(b))
}

// AddRivalry records a rivalry note between two players. Admin only.
func (s *Service) AddRivalry(by *models.User, aID, bID int64, note string) error {
	if err := requireAdmin(by); err != nil {
		return err
	}
	note = strings.TrimSpace(note)
	if note == "" {
		return errors.New("a rivalry note is required")
	}
	if aID == bID {
		return errors.New("pick two different players")
	}
	c := s.loadStoredContext()
	c.Rivalries = append(c.Rivalries, storedRivalry{A: aID, B: bID, Note: note})
	return s.saveStoredContext(c)
}

// DeleteRivalry removes the rivalry at idx (as ordered by CommentContextView). Admin only.
func (s *Service) DeleteRivalry(by *models.User, idx int) error {
	if err := requireAdmin(by); err != nil {
		return err
	}
	c := s.loadStoredContext()
	if idx < 0 || idx >= len(c.Rivalries) {
		return errors.New("no such rivalry")
	}
	c.Rivalries = append(c.Rivalries[:idx], c.Rivalries[idx+1:]...)
	return s.saveStoredContext(c)
}

// AddCommentNote records a general (house) context note. Admin only.
func (s *Service) AddCommentNote(by *models.User, note string) error {
	if err := requireAdmin(by); err != nil {
		return err
	}
	note = strings.TrimSpace(note)
	if note == "" {
		return errors.New("a note is required")
	}
	c := s.loadStoredContext()
	c.Notes = append(c.Notes, note)
	return s.saveStoredContext(c)
}

// DeleteCommentNote removes the note at idx. Admin only.
func (s *Service) DeleteCommentNote(by *models.User, idx int) error {
	if err := requireAdmin(by); err != nil {
		return err
	}
	c := s.loadStoredContext()
	if idx < 0 || idx >= len(c.Notes) {
		return errors.New("no such note")
	}
	c.Notes = append(c.Notes[:idx], c.Notes[idx+1:]...)
	return s.saveStoredContext(c)
}

// RivalryView is a rivalry resolved to display names, for the admin editor.
type RivalryView struct {
	AID, BID int64
	A, B     string
	Note     string
}

// CommentContextView is the admin-facing view of the stored comment context.
type CommentContextView struct {
	Rivalries []RivalryView
	Notes     []string
}

// CommentContextView returns the stored rivalries (resolved to names) + notes. Admin only.
func (s *Service) CommentContextView(by *models.User) (CommentContextView, error) {
	if err := requireAdmin(by); err != nil {
		return CommentContextView{}, err
	}
	c := s.loadStoredContext()
	names := s.userNameMap()
	var v CommentContextView
	for _, r := range c.Rivalries {
		v.Rivalries = append(v.Rivalries, RivalryView{
			AID: r.A, BID: r.B, A: names[r.A], B: names[r.B], Note: r.Note,
		})
	}
	v.Notes = append(v.Notes, c.Notes...)
	return v, nil
}

func (s *Service) userNameMap() map[int64]string {
	m := map[int64]string{}
	users, err := s.store.AllUsers()
	if err != nil {
		return m
	}
	for _, u := range users {
		m[u.ID] = u.DisplayName
	}
	return m
}

// CommentConfig builds the comment worker's config from the stored settings:
// the global default tone, per-player tone overrides (keyed by display name), and
// the admin context (rivalries resolved to names + house notes). This is the
// worker seam (wired in main.go) — not admin-gated, since the worker, not a user,
// is the caller. Self is filled in by the worker.
func (s *Service) CommentConfig() ai.CommentConfig {
	def, _ := s.CommentTone()
	users, _ := s.store.AllUsers()
	names := make(map[int64]string, len(users))
	tones := map[string]string{}
	for _, u := range users {
		names[u.ID] = u.DisplayName
		if t := s.userToneOverride(u.ID); t != "" {
			tones[u.DisplayName] = t
		}
	}
	c := s.loadStoredContext()
	var rivalries []ai.Rivalry
	for _, r := range c.Rivalries {
		a, b := names[r.A], names[r.B]
		if a == "" || b == "" {
			continue // a participant no longer exists — skip
		}
		rivalries = append(rivalries, ai.Rivalry{A: a, B: b, Note: r.Note})
	}
	return ai.CommentConfig{
		DefaultTone: def,
		ToneByName:  tones,
		Rivalries:   rivalries,
		Notes:       c.Notes,
	}
}
