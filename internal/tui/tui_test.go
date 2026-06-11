package tui

import (
	"bytes"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"bethoven/internal/clock"
	"bethoven/internal/db"
	"bethoven/internal/service"
)

const testInvite = "secret"

func newTestService(t *testing.T) (*service.Service, *db.Store, *clock.Fake) {
	t.Helper()
	conn, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	store := db.NewStore(conn)
	fc := &clock.Fake{T: time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)}
	tid, _ := store.CreateTournament("Test Cup", true, fc.Now())
	return service.New(store, fc, testInvite, nil, tid), store, fc
}

// TestRegistrationFlow drives the Bubble Tea model directly: type the invite
// code, tab to the name field, type a name, hit enter, and confirm we land on
// the main menu — and that the user was persisted.
func TestRegistrationFlow(t *testing.T) {
	svc, _, _ := newTestService(t)
	m := New(svc, "SHA256:newkey", false, nil)

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 40))

	tm.Type(testInvite)
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	tm.Type("Alice")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("Place / edit bets")) && bytes.Contains(b, []byte("Alice"))
	}, teatest.WithDuration(3*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))

	if u, err := svc.Lookup("SHA256:newkey"); err != nil || u.DisplayName != "Alice" {
		t.Errorf("expected Alice persisted, got %+v err=%v", u, err)
	}
}

// TestRegistrationRejectsBadCode: a wrong invite code keeps you on the
// registration screen with an error, and creates no user.
func TestRegistrationRejectsBadCode(t *testing.T) {
	svc, _, _ := newTestService(t)
	m := New(svc, "SHA256:badkey", false, nil)

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 40))
	tm.Type("wrongcode")
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	tm.Type("Mallory")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("invalid invite code"))
	}, teatest.WithDuration(3*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))

	if u, _ := svc.Lookup("SHA256:badkey"); u != nil {
		t.Errorf("bad code must not create a user, got %+v", u)
	}
}
