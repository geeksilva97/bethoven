package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"bethoven/internal/ai"
)

// betOK places a bet that must succeed (helper for the history test).
func betOK(t *testing.T, svc *Service, uid, mid int64, a, b int) {
	t.Helper()
	if err := svc.PlaceBet(uid, mid, int64(a), int64(b)); err != nil {
		t.Fatalf("PlaceBet u=%d m=%d: %v", uid, mid, err)
	}
}

// fakeCommentMonitor is a minimal AICommentMonitor for testing the read-time feed.
type fakeCommentMonitor struct{ acts []ai.CommentAction }

func (f fakeCommentMonitor) Status() ai.CommentStatus { return ai.CommentStatus{} }
func (f fakeCommentMonitor) Activity(limit int) []ai.CommentAction {
	if limit > 0 && limit < len(f.acts) {
		return f.acts[:limit]
	}
	return f.acts
}

// TestCommentMood covers the self-evolving mood setting: default, validation,
// persistence, and the unchanged-value no-op (director writes it every pass).
func TestCommentMood(t *testing.T) {
	svc, _, _ := newTestService(t)

	if m, _ := svc.CommentMood(); m != "neutral" {
		t.Fatalf("default mood = %q, want neutral", m)
	}
	if err := svc.SetCommentMood("nope"); err == nil {
		t.Fatal("invalid mood should be rejected")
	}
	if err := svc.SetCommentMood("Cocky"); err != nil { // case-insensitive
		t.Fatalf("SetCommentMood: %v", err)
	}
	if m, _ := svc.CommentMood(); m != "cocky" {
		t.Fatalf("mood = %q, want cocky", m)
	}
	// Unchanged value is a no-op (must not error), and the mood reaches CommentConfig.
	if err := svc.SetCommentMood("cocky"); err != nil {
		t.Fatalf("re-setting the same mood should be a no-op, got %v", err)
	}
	if cfg := svc.CommentConfig(); cfg.Mood != "cocky" {
		t.Fatalf("CommentConfig.Mood = %q, want cocky", cfg.Mood)
	}
}

// TestAICommentActivityHidesMuted checks a muted player's lingering pre-mute entry
// is dropped from the admin feed at read time, while errors (no player) survive.
func TestAICommentActivityHidesMuted(t *testing.T) {
	svc, _, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, testInvite, "Admin")
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	svc.Register("SHA256:bob", testInvite, "Bob")

	svc.SetCommentMonitor(fakeCommentMonitor{acts: []ai.CommentAction{
		{Player: "Alice", Text: "alice line", Outcome: "written"},
		{Player: "Bob", Text: "bob line", Outcome: "written"},
		{Player: "", Outcome: "error", Err: "boom"},
	}})

	// Before muting, all three entries are visible.
	if got, _ := svc.AICommentActivity(admin, 0); len(got) != 3 {
		t.Fatalf("expected 3 entries before mute, got %d", len(got))
	}

	// Mute Alice -> her lingering entry vanishes; Bob + the error remain.
	if err := svc.SetUserCommentTone(admin, alice.ID, "mute"); err != nil {
		t.Fatal(err)
	}
	got, err := svc.AICommentActivity(admin, 0)
	if err != nil {
		t.Fatalf("AICommentActivity: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("muted Alice must be dropped, got %d entries: %+v", len(got), got)
	}
	for _, a := range got {
		if a.Player == "Alice" {
			t.Errorf("muted Alice must not appear: %+v", a)
		}
	}
}

// psByName returns the standing for a named player in a round (fails if absent).
func psByName(t *testing.T, r ai.RoundStanding, name string) ai.PlayerStanding {
	t.Helper()
	for _, p := range r.Ranks {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("player %q not in round %q", name, r.Label)
	return ai.PlayerStanding{}
}

// TestStandingsHistoryMovements reconstructs a two-round history from finished
// matches alone (no stored standings) and checks positions + per-round movement
// and points-gained deltas — the grounded numbers BETanIA's narrative layer needs.
func TestStandingsHistoryMovements(t *testing.T) {
	svc, store, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, testInvite, "Admin")
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")

	day1 := base.Add(2 * time.Hour)  // 2026-06-11
	day2 := base.Add(26 * time.Hour) // 2026-06-12
	m1 := addMatch(t, store, svc.tournamentID, day1)
	m2 := addMatch(t, store, svc.tournamentID, day2)

	// All bets are placed before any kickoff (clock = base).
	betOK(t, svc, alice.ID, m1, 2, 1) // will be exact
	betOK(t, svc, bob.ID, m1, 1, 0)   // right result only
	betOK(t, svc, alice.ID, m2, 1, 1) // wrong
	betOK(t, svc, bob.ID, m2, 0, 3)   // will be exact

	// Results (admin; not time-gated). Classic scoring: exact=3, result=1, miss=0.
	if err := svc.EnterResult(admin, m1, 2, 1); err != nil {
		t.Fatalf("EnterResult m1: %v", err)
	}
	if err := svc.EnterResult(admin, m2, 0, 3); err != nil {
		t.Fatalf("EnterResult m2: %v", err)
	}

	hist, err := svc.StandingsHistory()
	if err != nil {
		t.Fatalf("StandingsHistory: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("expected 2 rounds, got %d", len(hist))
	}

	// Round 1 (Jun 11): Alice 3 (#1), Bob 1 (#2); first round ⇒ movement 0.
	if a := psByName(t, hist[0], "Alice"); a.Position != 1 || a.Total != 3 || a.Movement != 0 || a.PointsGained != 3 {
		t.Errorf("R1 Alice = %+v", a)
	}
	if b := psByName(t, hist[0], "Bob"); b.Position != 2 || b.Total != 1 || b.Movement != 0 || b.PointsGained != 1 {
		t.Errorf("R1 Bob = %+v", b)
	}

	// Round 2 (Jun 12): Bob 4 (#1, climbed +1, +3), Alice 3 (#2, fell -1, +0).
	if b := psByName(t, hist[1], "Bob"); b.Position != 1 || b.Total != 4 || b.Movement != 1 || b.PointsGained != 3 {
		t.Errorf("R2 Bob = %+v", b)
	}
	if a := psByName(t, hist[1], "Alice"); a.Position != 2 || a.Total != 3 || a.Movement != -1 || a.PointsGained != 0 {
		t.Errorf("R2 Alice = %+v", a)
	}
}

// TestCommentConfigTonesAndContext checks the worker config the service builds:
// default tone, per-player overrides (incl. mute), and resolved rivalries/notes —
// plus that "default" clears an override and deletes drop entries.
func TestCommentConfigTonesAndContext(t *testing.T) {
	svc, _, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, testInvite, "Admin")
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")

	if err := svc.SetCommentTone(admin, "savage"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetUserCommentTone(admin, alice.ID, "playful"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetUserCommentTone(admin, bob.ID, "mute"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddRivalry(admin, alice.ID, bob.ID, "office rivals"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddCommentNote(admin, "loser buys lunch"); err != nil {
		t.Fatal(err)
	}

	cfg := svc.CommentConfig()
	if cfg.DefaultTone != "savage" {
		t.Errorf("default tone = %q", cfg.DefaultTone)
	}
	if cfg.ToneByName["Alice"] != "playful" || cfg.ToneByName["Bob"] != "mute" {
		t.Errorf("tone overrides = %v", cfg.ToneByName)
	}
	if len(cfg.Rivalries) != 1 || cfg.Rivalries[0].A != "Alice" || cfg.Rivalries[0].B != "Bob" || cfg.Rivalries[0].Note != "office rivals" {
		t.Errorf("rivalries = %+v", cfg.Rivalries)
	}
	if len(cfg.Notes) != 1 || cfg.Notes[0] != "loser buys lunch" {
		t.Errorf("notes = %+v", cfg.Notes)
	}

	// "default" clears the override.
	if err := svc.SetUserCommentTone(admin, alice.ID, "default"); err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.CommentConfig().ToneByName["Alice"]; ok {
		t.Error(`"default" should clear Alice's override`)
	}

	// Edits replace the text in place (participants unchanged for a rivalry).
	if err := svc.EditRivalry(admin, 0, "fierce office rivals"); err != nil {
		t.Fatal(err)
	}
	if err := svc.EditCommentNote(admin, 0, "loser buys dinner"); err != nil {
		t.Fatal(err)
	}
	cfg = svc.CommentConfig()
	if cfg.Rivalries[0].Note != "fierce office rivals" || cfg.Rivalries[0].A != "Alice" {
		t.Errorf("edited rivalry = %+v", cfg.Rivalries[0])
	}
	if cfg.Notes[0] != "loser buys dinner" {
		t.Errorf("edited note = %q", cfg.Notes[0])
	}
	// Edit rejects an empty text and an out-of-range index.
	if err := svc.EditCommentNote(admin, 0, "   "); err == nil {
		t.Error("empty note edit should fail")
	}
	if err := svc.EditRivalry(admin, 9, "x"); err == nil {
		t.Error("out-of-range rivalry edit should fail")
	}

	// Deletes remove the entries.
	if err := svc.DeleteRivalry(admin, 0); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteCommentNote(admin, 0); err != nil {
		t.Fatal(err)
	}
	if cfg := svc.CommentConfig(); len(cfg.Rivalries) != 0 || len(cfg.Notes) != 0 {
		t.Errorf("expected empty after delete: %+v / %+v", cfg.Rivalries, cfg.Notes)
	}
}

func TestCommentConfigAdminGated(t *testing.T) {
	svc, _, _ := newTestService(t)
	player, _ := svc.Register("SHA256:p", testInvite, "Player")
	if err := svc.SetUserCommentTone(player, player.ID, "savage"); err == nil {
		t.Error("non-admin SetUserCommentTone should be rejected")
	}
	if err := svc.AddCommentNote(player, "x"); err == nil {
		t.Error("non-admin AddCommentNote should be rejected")
	}
	if err := svc.AddRivalry(player, 1, 2, "x"); err == nil {
		t.Error("non-admin AddRivalry should be rejected")
	}
	if err := svc.AddPlayerNote(player, player.ID, "x"); err == nil {
		t.Error("non-admin AddPlayerNote should be rejected")
	}
}

// TestPlayerNotes covers the player-bound house-note tier: add (with player
// validation), the resolved CommentConfig + admin view, edit/delete by index, and
// that a note whose player was removed is dropped from the model-facing config but
// kept (with a blank name) in the admin view so editor indices stay aligned.
func TestPlayerNotes(t *testing.T) {
	svc, _, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, testInvite, "Admin")
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")

	// A note bound to a non-existent player is rejected.
	if err := svc.AddPlayerNote(admin, 9999, "ghost"); err == nil {
		t.Error("AddPlayerNote for a missing player should fail")
	}
	// Empty text is rejected.
	if err := svc.AddPlayerNote(admin, alice.ID, "  "); err == nil {
		t.Error("empty player note should fail")
	}

	if err := svc.AddPlayerNote(admin, alice.ID, "always bets late"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddPlayerNote(admin, bob.ID, "Barcelona diehard"); err != nil {
		t.Fatal(err)
	}

	cfg := svc.CommentConfig()
	if len(cfg.PlayerNotes) != 2 {
		t.Fatalf("player notes = %+v", cfg.PlayerNotes)
	}
	if cfg.PlayerNotes[0].Player != "Alice" || cfg.PlayerNotes[0].Text != "always bets late" {
		t.Errorf("note 0 = %+v", cfg.PlayerNotes[0])
	}
	if cfg.PlayerNotes[1].Player != "Bob" || cfg.PlayerNotes[1].Text != "Barcelona diehard" {
		t.Errorf("note 1 = %+v", cfg.PlayerNotes[1])
	}

	// Admin view resolves names in stored order.
	v, err := svc.CommentContextView(admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.PlayerNotes) != 2 || v.PlayerNotes[0].Player != "Alice" || v.PlayerNotes[1].Player != "Bob" {
		t.Errorf("view player notes = %+v", v.PlayerNotes)
	}

	// Edit replaces the text in place; player unchanged.
	if err := svc.EditPlayerNote(admin, 0, "bets at the last second"); err != nil {
		t.Fatal(err)
	}
	if got := svc.CommentConfig().PlayerNotes[0]; got.Player != "Alice" || got.Text != "bets at the last second" {
		t.Errorf("edited note = %+v", got)
	}
	if err := svc.EditPlayerNote(admin, 9, "x"); err == nil {
		t.Error("out-of-range player-note edit should fail")
	}

	// Delete by index removes one.
	if err := svc.DeletePlayerNote(admin, 0); err != nil {
		t.Fatal(err)
	}
	if cfg := svc.CommentConfig(); len(cfg.PlayerNotes) != 1 || cfg.PlayerNotes[0].Player != "Bob" {
		t.Errorf("after delete = %+v", cfg.PlayerNotes)
	}
}

// TestCompactCommentNotes covers fusing the admin's free-text house notes: the
// worker-attached path (uses the compactor's output), admin gating, the <2 no-op, and
// the nil-compactor lossless-join fallback.
func TestCompactCommentNotes(t *testing.T) {
	svc, _, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, testInvite, "Admin")
	player, _ := svc.Register("SHA256:p", testInvite, "Pleb")

	// Player can't compact.
	if err := svc.CompactCommentNotes(player); !errors.Is(err, ErrForbidden) {
		t.Fatalf("player CompactCommentNotes: want ErrForbidden, got %v", err)
	}

	// Fewer than two notes ⇒ no-op (leaves the single note intact).
	if err := svc.AddCommentNote(admin, "only one"); err != nil {
		t.Fatal(err)
	}
	if err := svc.CompactCommentNotes(admin); err != nil {
		t.Fatalf("compact with one note: %v", err)
	}
	if v, _ := svc.CommentContextView(admin); len(v.Notes) != 1 {
		t.Fatalf("one-note compact changed the notes: %+v", v.Notes)
	}

	if err := svc.AddCommentNote(admin, "two"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddCommentNote(admin, "three"); err != nil {
		t.Fatal(err)
	}

	// With a compactor attached, the fused note is the compactor's output.
	var got []string
	svc.SetHouseNotesCompactor(func(_ context.Context, notes []string) (string, error) {
		got = notes
		return "fused house note", nil
	})
	if err := svc.CompactCommentNotes(admin); err != nil {
		t.Fatalf("CompactCommentNotes: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("compactor saw %d notes, want 3", len(got))
	}
	v, _ := svc.CommentContextView(admin)
	if len(v.Notes) != 1 || v.Notes[0] != "fused house note" {
		t.Fatalf("after compact: %+v", v.Notes)
	}

	// A compactor error leaves the notes untouched (never destroys them).
	if err := svc.AddCommentNote(admin, "another"); err != nil {
		t.Fatal(err)
	}
	svc.SetHouseNotesCompactor(func(_ context.Context, _ []string) (string, error) {
		return "", errors.New("model down")
	})
	if err := svc.CompactCommentNotes(admin); err == nil {
		t.Fatal("compact should surface the model error")
	}
	if v, _ := svc.CommentContextView(admin); len(v.Notes) != 2 {
		t.Fatalf("notes lost on compactor error: %+v", v.Notes)
	}

	// No compactor ⇒ lossless join into a single note (nothing dropped).
	svc.SetHouseNotesCompactor(nil)
	if err := svc.CompactCommentNotes(admin); err != nil {
		t.Fatalf("nil-compactor compact: %v", err)
	}
	v, _ = svc.CommentContextView(admin)
	if len(v.Notes) != 1 || v.Notes[0] != "fused house note\nanother" {
		t.Fatalf("lossless join = %+v", v.Notes)
	}
}

type fakeCommentSource struct{ m map[int64]ai.Comment }

func (f fakeCommentSource) All(now time.Time) map[int64]ai.Comment { return f.m }

// TestLeaderboardCommentsScoping asserts the visibility model: on the main
// leaderboard EVERYONE sees only their own comment, while AllLeaderboardComments
// (the cycle source) is open to all and returns the full non-muted set. No source ⇒ none.
func TestLeaderboardCommentsScoping(t *testing.T) {
	svc, _, _ := newTestService(t)
	admin, _ := svc.Register(adminFP, testInvite, "Admin")
	alice, _ := svc.Register("SHA256:alice", testInvite, "Alice")
	bob, _ := svc.Register("SHA256:bob", testInvite, "Bob")

	if got := svc.LeaderboardComments(alice); len(got) != 0 {
		t.Fatalf("no source should yield no comments, got %v", got)
	}

	svc.SetCommentSource(fakeCommentSource{m: map[int64]ai.Comment{
		admin.ID: {UserID: admin.ID, Text: "admin line"},
		alice.ID: {UserID: alice.ID, Text: "alice line"},
		bob.ID:   {UserID: bob.ID, Text: "bob line"},
	}})

	if got := svc.LeaderboardComments(alice); len(got) != 1 || got[alice.ID] != "alice line" {
		t.Fatalf("player should see only own comment, got %v", got)
	}
	// The admin's main-leaderboard view is own-only too.
	if got := svc.LeaderboardComments(admin); len(got) != 1 || got[admin.ID] != "admin line" {
		t.Fatalf("admin should see only own comment on the leaderboard, got %v", got)
	}
	// The full set (cycle source) is open to all — a player gets everyone's.
	if got := svc.AllLeaderboardComments(); len(got) != 3 {
		t.Fatalf("AllLeaderboardComments should return all 3, got %v", got)
	}

	// Muting a player hides their ALREADY-CACHED comment immediately (read-time
	// enforcement) from both their own view and the full cycle set.
	if err := svc.SetUserCommentTone(admin, bob.ID, "mute"); err != nil {
		t.Fatal(err)
	}
	if got := svc.AllLeaderboardComments(); len(got) != 2 || got[bob.ID] != "" {
		t.Fatalf("muted Bob must vanish from the full set immediately, got %v", got)
	}
	if got := svc.LeaderboardComments(bob); len(got) != 0 {
		t.Fatalf("muted Bob must not see his own cached comment, got %v", got)
	}
	// A non-muted player is unaffected.
	if got := svc.LeaderboardComments(alice); got[alice.ID] != "alice line" {
		t.Fatalf("Alice should still see her comment, got %v", got)
	}
}
