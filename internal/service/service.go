// Package service holds BEThoven's business rules, decoupled from SSH and the
// TUI. It depends only on the store and an injected clock, which makes every
// rule (onboarding, the kickoff lock, scoring) testable against a real DB with
// deterministic time.
package service

import (
	"errors"
	"strings"
	"time"

	"bethoven/internal/auth"
	"bethoven/internal/clock"
	"bethoven/internal/db"
	"bethoven/internal/models"
)

// Service-level sentinel errors, surfaced to the TUI as friendly messages.
var (
	ErrBadInvite    = errors.New("invalid invite code")
	ErrNameRequired = errors.New("a display name is required")
)

// Service bundles the dependencies the business logic needs.
type Service struct {
	store        *db.Store
	clock        clock.Clock
	inviteCode   string
	admins       []string
	tournamentID int64
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

// Resolve is what the server calls on each connect. It looks up the user and,
// if their key is in the admin allowlist but they aren't an admin yet,
// auto-promotes them. This keeps admin setup foolproof: add the fingerprint to
// BETHOVEN_ADMINS and connect — order doesn't matter. Returns db.ErrNotFound
// for an unknown key.
func (s *Service) Resolve(fingerprint string) (*models.User, error) {
	u, err := s.store.UserByFingerprint(fingerprint)
	if err != nil {
		return nil, err
	}
	if s.IsAdmin(fingerprint) && u.Role != models.RoleAdmin {
		if err := s.store.SetUserRole(u.ID, models.RoleAdmin); err != nil {
			return nil, err
		}
		u.Role = models.RoleAdmin
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

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrNameRequired
	}

	role := models.RolePlayer
	if s.IsAdmin(fingerprint) {
		role = models.RoleAdmin
	} else if !auth.ValidInvite(code, s.inviteCode) {
		return nil, ErrBadInvite
	}

	return s.store.CreateUser(fingerprint, name, role, s.clock.Now().UTC())
}
