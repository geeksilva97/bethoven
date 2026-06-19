// Package service holds BEThoven's business rules, decoupled from SSH and the
// TUI. It depends only on the store and an injected clock, which makes every
// rule (onboarding, the kickoff lock, scoring) testable against a real DB with
// deterministic time.
package service

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"bethoven/internal/auth"
	"bethoven/internal/clock"
	"bethoven/internal/db"
	"bethoven/internal/live"
	"bethoven/internal/models"
)

// Service-level sentinel errors, surfaced to the TUI as friendly messages.
var (
	ErrBadInvite    = errors.New("invalid invite code")
	ErrNameRequired = errors.New("a display name is required")
	ErrBadName      = errors.New("display name has invalid characters")
	ErrNameTaken    = errors.New("that display name is already taken")
)

// maxNameLen bounds a display name (also guarded by the input widget).
const maxNameLen = 32

// cleanName trims a display name and rejects it if, once control characters and
// ANSI escapes are removed, nothing usable remains or it changed (i.e. it
// contained such characters). Names are rendered verbatim into OTHER players'
// terminals (leaderboard, per-match ranking, admin grid), so an unsanitized
// name could inject cursor/color/clear escapes into their screens. We reject
// rather than silently strip so the registrant sees what they'll actually get.
func cleanName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", ErrNameRequired
	}
	if utf8.RuneCountInString(name) > maxNameLen {
		return "", ErrBadName
	}
	for _, r := range name {
		// Reject C0/C1 control codes (incl. ESC 0x1b) and unprintable runes.
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) || !unicode.IsPrint(r) {
			return "", ErrBadName
		}
	}
	return name, nil
}

// LiveStore exposes the current in-memory live snapshot, keyed by match ID. The
// poller writes it; the service reads it to fold provisional points into the
// leaderboard and to overlay running scores. It is a port: nil is valid and
// means "no live feed", in which case the service behaves exactly as before.
type LiveStore interface {
	Snapshot() map[int64]live.Score
}

// Service bundles the dependencies the business logic needs.
type Service struct {
	store        *db.Store
	clock        clock.Clock
	inviteCode   string
	admins       []string
	tournamentID int64
	live         LiveStore // optional; nil when no live feed is configured
	// analytics is the optional usage tracker; nil when analytics is disabled,
	// in which case every emit site is a cheap no-op (see track / trackByID).
	analytics AnalyticsSink
	// ai is the optional BETanIA live-worker monitor; nil when BETanIA isn't
	// running, in which case the admin AI reads return ErrAIOff.
	ai AIMonitor
	// aiTrigger is the optional "run now" hook into the live worker; nil when
	// BETanIA isn't running, in which case TriggerAI returns ErrAIOff.
	aiTrigger func() bool
	// comments is the optional BETanIA leaderboard-comment cache; nil when the
	// comment worker isn't running, in which case the leaderboard shows no comments.
	comments CommentSource
	// liveComments is the optional BETanIA live-commentary cache (the single
	// top-of-board line shown while a match is in play); nil when the live-comment
	// worker isn't running, in which case the leaderboard shows no live line.
	liveComments LiveCommentSource
	// aiComments / aiCommentTrigger are the comment worker's admin observability
	// and "run now" hooks; nil when it isn't running (admin reads return ErrAIOff).
	aiComments       AICommentMonitor
	aiCommentTrigger func() bool
	// aiUsage is the optional persistent reader for BETanIA's Claude token usage and
	// estimated cost (backed by the on-disk usage log); nil when BETanIA isn't running.
	aiUsage AIUsageSource
	// forms holds each team's pre-tournament recent-form baseline (oldest→newest),
	// seeded from fixtures.json. Optional; nil when no baseline is configured.
	forms map[string][]models.FormOutcome
}

// New builds a Service bound to the active tournament.
func New(store *db.Store, clk clock.Clock, inviteCode string, admins []string, tournamentID int64) *Service {
	return &Service{
		store:        store,
		clock:        clk,
		inviteCode:   inviteCode,
		admins:       admins,
		tournamentID: tournamentID,
	}
}

// SetLiveStore attaches a live snapshot source (the poller's cache). Optional —
// when unset, the leaderboard and fixtures behave as if nothing is in play.
func (s *Service) SetLiveStore(ls LiveStore) { s.live = ls }

// SetAnalyticsSink attaches the usage tracker. Optional — when unset, every emit
// site is a no-op and the service behaves exactly as if analytics didn't exist.
func (s *Service) SetAnalyticsSink(a AnalyticsSink) { s.analytics = a }

// liveSnapshot returns the current live scores, or nil when no feed is attached.
func (s *Service) liveSnapshot() map[int64]live.Score {
	if s.live == nil {
		return nil
	}
	return s.live.Snapshot()
}

// overlayLive fills a match's read-time Live* fields from the snapshot when the
// match is in play. Finished matches and matches without a live entry are left
// untouched (their authoritative result already stands).
func overlayLive(m *models.Match, snap map[int64]live.Score) {
	if snap == nil || m.Finished {
		return
	}
	ls, ok := snap[m.ID]
	if !ok || ls.State != live.StateIn {
		return
	}
	m.Live = true
	m.LiveScoreA, m.LiveScoreB = ls.A, ls.B
	m.LiveMinute, m.LiveClock = ls.Minute, ls.Clock
	m.LivePhase = ls.Phase
	m.LiveOdds = ls.Odds
	m.LiveEvents = ls.Events
}

// IsAdmin reports whether a key fingerprint is in the admin allowlist. The TUI
// uses this to skip the invite-code prompt for admins on first connect.
func (s *Service) IsAdmin(fingerprint string) bool {
	return auth.IsAdmin(fingerprint, s.admins)
}

// Now returns the server's authoritative time in UTC. The TUI uses it to render
// lock state consistently with the kickoff lock the service enforces.
func (s *Service) Now() time.Time {
	return s.clock.Now().UTC()
}

// Lookup resolves a key fingerprint to its user, returning db.ErrNotFound for
// an unknown key (which the TUI turns into the registration flow).
func (s *Service) Lookup(fingerprint string) (*models.User, error) {
	return s.store.UserByFingerprint(fingerprint)
}

// Resolve is what the server calls on each connect. It reconciles the user's
// stored role with the admin allowlist in BOTH directions, treating
// BETHOVEN_ADMINS as the single source of truth:
//
//   - a key in the allowlist is promoted to admin (so admin setup is foolproof:
//     add the fingerprint and connect, order doesn't matter), and
//   - a stored admin whose key is no longer in the allowlist is demoted to
//     player (so removing a fingerprint actually revokes admin).
//
// Returns db.ErrNotFound for an unknown key.
func (s *Service) Resolve(fingerprint string) (*models.User, error) {
	u, err := s.store.UserByFingerprint(fingerprint)
	if err != nil {
		return nil, err
	}

	want := models.RolePlayer
	if s.IsAdmin(fingerprint) {
		want = models.RoleAdmin
	}
	if u.Role != want {
		if err := s.store.SetUserRole(u.ID, want); err != nil {
			return nil, err
		}
		u.Role = want
	}
	return u, nil
}

// Register binds an unknown key to a new account. Admins skip the invite code;
// everyone else must supply the correct one. Idempotent: if the key is already
// registered it returns the existing user.
func (s *Service) Register(fingerprint, code, name string) (*models.User, error) {
	if existing, err := s.store.UserByFingerprint(fingerprint); err == nil {
		return existing, nil
	} else if !errors.Is(err, db.ErrNotFound) {
		return nil, err
	}

	name, err := cleanName(name)
	if err != nil {
		return nil, err
	}
	if taken, err := s.store.DisplayNameTaken(name); err != nil {
		return nil, err
	} else if taken {
		return nil, ErrNameTaken
	}

	role := models.RolePlayer
	if s.IsAdmin(fingerprint) {
		role = models.RoleAdmin
	} else if !auth.ValidInvite(code, s.inviteCode) {
		return nil, ErrBadInvite
	}

	u, err := s.store.CreateUser(fingerprint, name, role, s.clock.Now().UTC())
	if err != nil {
		return nil, err
	}
	s.track(u, fingerprint, EvRegistered, map[string]string{"role": string(role)})
	return u, nil
}
