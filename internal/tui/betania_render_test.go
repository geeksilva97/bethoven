package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/ai"
	"bethoven/internal/models"
	"bethoven/internal/service"
)

// keyMsg builds a tea.KeyMsg for the special keys / single runes the tests press.
func keyMsg(key string) tea.KeyMsg {
	switch key {
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
}

// adminModel builds a bare admin Model for render/logic smoke tests (no terminal).
func adminModel(t *testing.T) Model {
	t.Helper()
	svc, _, _ := newTestService(t)
	admin := &models.User{ID: 1, DisplayName: "Boss", Role: models.RoleAdmin}
	m := New(svc, "SHA256:boss", true, admin)
	m.width = 120
	m.standings = []service.Standing{
		{User: models.User{ID: 1, DisplayName: "Boss"}, Total: 30},
		{User: models.User{ID: 2, DisplayName: "Alice"}, Total: 20},
		{User: models.User{ID: 3, DisplayName: "Bob"}, Total: 10},
	}
	return m
}

// TestLeaderboardCycleRender checks the admin comment-cycle shows exactly the
// current player's comment (not everyone's) and the cycling hint.
func TestLeaderboardCycleRender(t *testing.T) {
	m := adminModel(t)
	m.screen = screenLeaderboard
	m.rowComments = map[int64]string{1: "your own line"}

	// Cycle OFF: own comment shows, no cycling hint.
	off := m.viewLeaderboard()
	if !strings.Contains(off, "your own line") || strings.Contains(off, "cycling takes") {
		t.Fatalf("cycle-off should show own comment and no cycling hint:\n%s", off)
	}

	// Cycle ON, pointing at Alice (id 2): only Alice's comment shows.
	m.cycleComments = true
	m.cycleAll = map[int64]string{1: "boss line", 2: "alice line", 3: "bob line"}
	m.cycleCurrentID = 2
	on := m.viewLeaderboard()
	if !strings.Contains(on, "alice line") {
		t.Errorf("cycling should show Alice's comment:\n%s", on)
	}
	if strings.Contains(on, "bob line") || strings.Contains(on, "boss line") || strings.Contains(on, "your own line") {
		t.Errorf("cycling must show ONLY the current player's comment:\n%s", on)
	}
	if !strings.Contains(on, "cycling takes — showing Alice") {
		t.Errorf("expected cycling hint naming Alice:\n%s", on)
	}
}

// TestCycleOnByDefault checks every user lands on the leaderboard with the comment
// cycle already running — except a muted user, who gets no cycle.
func TestCycleOnByDefault(t *testing.T) {
	svc, store, _ := newTestService(t)

	player, _ := svc.Register("SHA256:p", testInvite, "Player")
	pm := New(svc, "SHA256:p", false, player)
	res, _ := pm.enterMenuItem(screenLeaderboard)
	if got := res.(Model); !got.cycleComments {
		t.Errorf("a regular player should enter with the cycle on")
	}

	// A muted player gets no cycle.
	admin := mustRegisterAdmin(t, svc, store)
	if err := svc.SetUserCommentTone(admin.user, player.ID, "mute"); err != nil {
		t.Fatal(err)
	}
	res2, _ := pm.enterMenuItem(screenLeaderboard)
	got2 := res2.(Model)
	if got2.cycleComments || !got2.selfMuted {
		t.Errorf("a muted player must not cycle (selfMuted=%v, cycle=%v)", got2.selfMuted, got2.cycleComments)
	}
}

// mustRegisterAdmin builds an admin-backed Model whose user exists in the store
// (so service reads resolve).
func mustRegisterAdmin(t *testing.T, svc *service.Service, store interface {
	CreateUser(string, string, models.Role, time.Time) (*models.User, error)
}) Model {
	t.Helper()
	u, err := store.CreateUser("SHA256:boss", "Boss", models.RoleAdmin, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	return New(svc, "SHA256:boss", true, u)
}

// TestHideCommentsToggle checks 'h' hides comments on the board, persists the
// preference, and that entering with it set keeps the cycle off.
func TestHideCommentsToggle(t *testing.T) {
	svc, _, _ := newTestService(t)
	u, _ := svc.Register("SHA256:u", testInvite, "U")
	m := New(svc, "SHA256:u", false, u)
	m.width = 120
	m.screen = screenLeaderboard
	m.standings = []service.Standing{{User: *u, Total: 10}}
	m.rowComments = map[int64]string{u.ID: "your line"}

	if !strings.Contains(m.viewLeaderboard(), "your line") {
		t.Fatalf("comment should be visible before hiding")
	}

	res, _ := m.updateLeaderboard(keyMsg("h"))
	hm := res.(Model)
	if !hm.hideComments {
		t.Errorf("'h' should set hideComments")
	}
	if !svc.LeaderboardCommentsHidden(u) {
		t.Errorf("hide preference must be persisted")
	}
	if strings.Contains(hm.viewLeaderboard(), "your line") {
		t.Errorf("comment must not render once hidden")
	}

	// Entering the leaderboard fresh respects the persisted preference (cycle off).
	res2, _ := m.enterMenuItem(screenLeaderboard)
	em := res2.(Model)
	if !em.hideComments || em.cycleComments {
		t.Errorf("entry should honor hide pref: hide=%v cycle=%v", em.hideComments, em.cycleComments)
	}
}

// TestCommentWidthCapped checks a long comment wraps into multiple lines (a squarer
// block) even on a very wide terminal, instead of one long line.
func TestCommentWidthCapped(t *testing.T) {
	m := adminModel(t)
	m.screen = screenLeaderboard
	m.width = 220 // very wide: without a cap this would be one line
	long := strings.Repeat("word ", 40)
	m.rowComments = map[int64]string{1: strings.TrimSpace(long)}

	out := m.viewLeaderboard()
	// The continuation indent (leaderCommentCol+3 spaces) only appears when the
	// comment wrapped past the first line.
	indent := strings.Repeat(" ", leaderCommentCol+3)
	if !strings.Contains(out, "\n"+indent+"word") {
		t.Errorf("a long comment should wrap into multiple lines on a wide terminal:\n%s", out)
	}
}

// TestCycleTickAdvances checks the tick rotates to a different player and that a
// stale epoch / leaving the screen stops the loop.
func TestCycleTickAdvances(t *testing.T) {
	m := adminModel(t)
	m.screen = screenLeaderboard
	m.cycleComments = true
	m.cycleAll = map[int64]string{1: "a", 2: "b", 3: "c"}
	m.cycleCurrentID = 2
	m.cycleEpoch = 5

	next, cmd := m.onCycleTick(cycleTickMsg{epoch: 5})
	nm := next.(Model)
	if nm.cycleCurrentID == 2 {
		t.Errorf("tick should advance to a different player, stayed at %d", nm.cycleCurrentID)
	}
	if cmd == nil {
		t.Error("a live tick should reschedule itself")
	}

	// Stale epoch -> no reschedule.
	if _, cmd := m.onCycleTick(cycleTickMsg{epoch: 1}); cmd != nil {
		t.Error("a superseded epoch must not reschedule")
	}
}

// TestCommentDetailNavigation checks the Comments tab cursor moves and enter
// opens the full-text detail screen with the selected comment.
func TestCommentDetailNavigation(t *testing.T) {
	m := adminModel(t)
	m.screen = screenBETanIA
	m.betaniaTab = tabComments
	m.aiCommentActivity = []ai.CommentAction{
		{Player: "Alice", Text: "short alice", Outcome: "written"},
		{Player: "Bob", Text: strings.Repeat("bob ", 60), Outcome: "written"},
	}

	// The selected row is marked, and previews are truncated.
	list := m.viewBETanIA()
	if !strings.Contains(list, "▸") {
		t.Errorf("comments tab should mark the selected row:\n%s", list)
	}

	// Move down then open detail.
	down := keyModel(t, m, "down")
	if down.aiCommentCursor != 1 {
		t.Fatalf("down should move cursor to 1, got %d", down.aiCommentCursor)
	}
	opened := keyModel(t, down, "enter")
	if opened.screen != screenAICommentDetail {
		t.Fatalf("enter should open the detail screen, got %v", opened.screen)
	}
	detail := opened.viewAICommentDetail()
	if !strings.Contains(detail, "Bob") || !strings.Contains(detail, "bob bob") {
		t.Errorf("detail should show Bob's full untruncated text:\n%s", detail)
	}
}

// keyModel feeds one key string to updateBETanIA and returns the resulting Model.
func keyModel(t *testing.T, m Model, key string) Model {
	t.Helper()
	next, _ := m.updateBETanIA(keyMsg(key))
	return next.(Model)
}

// TestBETanIAPanelFitsTerminal is the regression for the title scrolling off the top:
// on EVERY tab, at a constrained height, with enough (multi-line) feed rows to force
// scrolling, the rendered frame must (1) fit within the terminal height and (2) still
// contain the title. Exercises both the initial and a scrolled state.
func TestBETanIAPanelFitsTerminal(t *testing.T) {
	now := time.Now()
	// A tall feed: 30 picks each with a rationale (two visual lines apiece).
	var bets []service.AIBet
	var acts []ai.Action
	for i := 0; i < 30; i++ {
		a := "Team" + string(rune('A'+i%26))
		b := "Team" + string(rune('a'+i%26))
		match := a + " vs " + b
		bets = append(bets, service.AIBet{
			Match: models.Match{TeamA: a, TeamB: b},
			Bet:   models.Bet{PredA: 1, PredB: 0, UpdatedAt: now},
		})
		acts = append(acts, ai.Action{Match: match, Rationale: strings.Repeat("reason ", 12), Outcome: "placed"})
	}
	var comments []ai.CommentAction
	for i := 0; i < 30; i++ {
		comments = append(comments, ai.CommentAction{
			Player: "Player" + string(rune('A'+i%26)), Text: strings.Repeat("roast ", 12), Outcome: "written",
		})
	}
	usage := ai.UsageReport{
		Categories: []ai.CategoryUsage{
			{Category: "bet", Calls: 10, InputTokens: 1000, OutputTokens: 500, EstCostUSD: 1.23, AvgLatencyMS: 800, LastAt: now},
			{Category: "comment", Calls: 8, InputTokens: 900, OutputTokens: 400, EstCostUSD: 0.98, AvgLatencyMS: 700, LastAt: now},
			{Category: "live", Calls: 20, InputTokens: 2000, OutputTokens: 600, EstCostUSD: 2.10, AvgLatencyMS: 600, LastAt: now},
			{Category: "digest", Calls: 4, InputTokens: 500, OutputTokens: 200, EstCostUSD: 0.40, AvgLatencyMS: 900, LastAt: now},
		},
		Total: ai.CategoryUsage{Calls: 42, InputTokens: 4400, OutputTokens: 1700, EstCostUSD: 4.71},
	}

	// 24 is the standard minimum terminal height (the tightest that still fits the
	// status block + a scrolling feed + footer); also check a roomy terminal.
	for _, height := range []int{24, 40} {
		for _, tab := range []struct {
			name string
			idx  int
		}{{"Betting", tabBetting}, {"Comments", tabComments}, {"Usage", tabUsage}} {
			m := adminModel(t)
			m.height = height
			m.screen = screenBETanIA
			m.betaniaTab = tab.idx
			m.aiBets, m.aiActivity = bets, acts
			m.aiCommentActivity = comments
			m.aiUsage = usage

			// Initial render, then a few scroll steps to exercise the windowed state.
			for _, steps := range []int{0, 5, 12} {
				mm := m
				for i := 0; i < steps; i++ {
					mm = keyModel(t, mm, "down")
				}
				frame := mm.viewBETanIA()
				if got := lineCount(frame); got > height {
					t.Errorf("%s tab @h=%d after %d↓: frame is %d rows > height %d (would scroll the title off):\n%s",
						tab.name, height, steps, got, height, frame)
				}
				if !strings.Contains(frame, "Admin: BETanIA") {
					t.Errorf("%s tab @h=%d after %d↓: title missing from frame:\n%s", tab.name, height, steps, frame)
				}
			}
		}
	}
}
