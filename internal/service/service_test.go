package service

import (
	"errors"
	"testing"
	"time"

	"bethoven/internal/clock"
	"bethoven/internal/db"
	"bethoven/internal/models"
)

const (
	testInvite = "secret-code"
	adminFP    = "SHA256:admin-key"
)

var base = time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

// newTestService spins up a real temp SQLite, an active tournament, and a
// Service wired to a fake clock. Returns the service, store, and clock so tests
// can inspect state and advance time.
func newTestService(t *testing.T) (*Service, *db.Store, *clock.Fake) {
	t.Helper()
	conn, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	store := db.NewStore(conn)

	fc := &clock.Fake{T: base}
	tid, err := store.CreateTournament("Test Cup", true, fc.Now())
	if err != nil {
		t.Fatalf("create tournament: %v", err)
	}
	svc := New(store, fc, testInvite, []string{adminFP}, tid)
	return svc, store, fc
}

func TestRegisterWithValidCode(t *testing.T) {
	svc, _, _ := newTestService(t)

	u, err := svc.Register("SHA256:alice", testInvite, "Alice")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if u.Role != models.RolePlayer {
		t.Errorf("expected player role, got %q", u.Role)
	}

	got, err := svc.Lookup("SHA256:alice")
	if err != nil || got.ID != u.ID {
		t.Errorf("lookup after register failed: %+v err=%v", got, err)
	}
}

func TestRegisterWrongCodeRejectedNoUser(t *testing.T) {
	svc, store, _ := newTestService(t)

	_, err := svc.Register("SHA256:mallory", "wrong", "Mallory")
	if !errors.Is(err, ErrBadInvite) {
		t.Fatalf("expected ErrBadInvite, got %v", err)
	}
	// No account must have been created.
	if _, err := store.UserByFingerprint("SHA256:mallory"); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("rejected registration must not create a user, got %v", err)
	}
}

func TestRegisterRequiresName(t *testing.T) {
	svc, _, _ := newTestService(t)
	if _, err := svc.Register("SHA256:bob", testInvite, "   "); !errors.Is(err, ErrNameRequired) {
		t.Errorf("expected ErrNameRequired, got %v", err)
	}
}

func TestAdminSkipsInviteCode(t *testing.T) {
	svc, _, _ := newTestService(t)

	if !svc.IsAdmin(adminFP) {
		t.Fatal("admin fingerprint not recognised")
	}
	// Admin registers with an empty/garbage code and still becomes admin.
	u, err := svc.Register(adminFP, "", "Boss")
	if err != nil {
		t.Fatalf("admin Register: %v", err)
	}
	if u.Role != models.RoleAdmin {
		t.Errorf("expected admin role, got %q", u.Role)
	}
}

func TestResolveAutoPromotesAdmin(t *testing.T) {
	svc, store, _ := newTestService(t)

	// A user who registered as a player BEFORE being added to the admin list.
	// Simulate by registering with a non-admin key, then resolving an
	// allowlisted key that was registered as a player.
	u, err := store.CreateUser(adminFP, "Boss", models.RolePlayer, base)
	if err != nil {
		t.Fatalf("seed player: %v", err)
	}
	if u.Role != models.RolePlayer {
		t.Fatal("setup: expected player role")
	}

	got, err := svc.Resolve(adminFP)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Role != models.RoleAdmin {
		t.Errorf("expected auto-promotion to admin, got %q", got.Role)
	}
	// And it persists.
	reloaded, _ := svc.Lookup(adminFP)
	if reloaded.Role != models.RoleAdmin {
		t.Errorf("promotion not persisted, got %q", reloaded.Role)
	}
}

func TestLookupUnknownKey(t *testing.T) {
	svc, _, _ := newTestService(t)
	if _, err := svc.Lookup("SHA256:ghost"); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestRegisterIsIdempotent(t *testing.T) {
	svc, _, _ := newTestService(t)
	first, err := svc.Register("SHA256:carol", testInvite, "Carol")
	if err != nil {
		t.Fatalf("first register: %v", err)
	}
	second, err := svc.Register("SHA256:carol", "ignored", "Different")
	if err != nil {
		t.Fatalf("second register: %v", err)
	}
	if first.ID != second.ID || second.DisplayName != "Carol" {
		t.Errorf("re-register should return existing user, got %+v", second)
	}
}
