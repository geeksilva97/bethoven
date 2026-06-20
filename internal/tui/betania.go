package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/ai"
	"bethoven/internal/service"
)

// betaniaActivityLimit caps the recent-activity feed on the BETanIA panel.
const betaniaActivityLimit = 15

// betaniaBetsLimit caps the durable picks-on-record feed (DB-sourced) on the
// Betting tab.
const betaniaBetsLimit = 20

// betaniaCommentLimit caps the Comments-tab feed. Larger than the picks feed so
// the whole field's current comments (one per player) are browsable + selectable.
const betaniaCommentLimit = 40

// BETanIA admin tabs.
const (
	tabBetting  = 0
	tabComments = 1
	tabUsage    = 2
)

// betaniaTabCount is the number of admin tabs to cycle through.
const betaniaTabCount = 3

// updateBETanIA handles the admin panel keys: "r" runs a betting pass; "c"
// regenerates ALL leaderboard comments; "t" toggles the comment tone; "q" quits;
// any other key returns to the menu. When BETanIA isn't running there's nothing to
// trigger, so any key backs out.
func (m Model) updateBETanIA(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch {
	case k.String() == "q":
		return m, tea.Quit
	case m.aiDisabled:
		return m.goMenu(), nil
	case k.String() == "tab" || k.String() == "right":
		// Cycle forward: Betting → Comments → Usage → Betting.
		m.betaniaTab = (m.betaniaTab + 1) % betaniaTabCount
		return m, nil
	case k.String() == "left" || k.String() == "shift+tab":
		// Cycle backward.
		m.betaniaTab = (m.betaniaTab + betaniaTabCount - 1) % betaniaTabCount
		return m, nil
	case k.String() == "r":
		switch err := m.svc.TriggerAI(m.user); {
		case err == nil:
			m.setStatus("BETanIA: running a betting pass now — picks appear here as they land", false)
		case errors.Is(err, service.ErrAIBusy):
			m.setStatus("BETanIA: a betting run is already in progress", false)
		default:
			m.setStatus(err.Error(), true)
		}
		m.refreshBETanIA()
		return m, nil
	case k.String() == "c":
		switch err := m.svc.TriggerAIComments(m.user); {
		case err == nil:
			m.setStatus("BETanIA: regenerating all leaderboard comments now", false)
		case errors.Is(err, service.ErrAIBusy):
			m.setStatus("BETanIA: a comment run is already in progress", false)
		case errors.Is(err, service.ErrAIOff):
			m.setStatus("BETanIA comments are not enabled", true)
		default:
			m.setStatus(err.Error(), true)
		}
		m.refreshBETanIA()
		return m, nil
	case k.String() == "t":
		next := "savage"
		if m.commentTone == "savage" {
			next = "playful"
		}
		if err := m.svc.SetCommentTone(m.user, next); err != nil {
			m.setStatus(err.Error(), true)
		} else {
			m.commentTone = next
			m.setStatus("BETanIA default tone set to "+next+" — press c to regenerate comments", false)
		}
		m.refreshBETanIA()
		return m, nil
	case k.String() == "u":
		return m.openAITones(), nil
	case k.String() == "x":
		return m.openAIContext(), nil
	case m.betaniaTab == tabBetting && (k.String() == "up" || k.String() == "k"):
		if m.aiBetsCursor > 0 {
			m.aiBetsCursor--
		}
		return m, nil
	case m.betaniaTab == tabBetting && (k.String() == "down" || k.String() == "j"):
		if m.aiBetsCursor < len(m.aiBets)-1 {
			m.aiBetsCursor++
		}
		return m, nil
	case m.betaniaTab == tabComments && k.String() == "s":
		return m.openAIPrompt(), nil
	case m.betaniaTab == tabComments && (k.String() == "up" || k.String() == "k"):
		if m.aiCommentCursor > 0 {
			m.aiCommentCursor--
		}
		return m, nil
	case m.betaniaTab == tabComments && (k.String() == "down" || k.String() == "j"):
		if m.aiCommentCursor < len(m.aiCommentActivity)-1 {
			m.aiCommentCursor++
		}
		return m, nil
	case m.betaniaTab == tabComments && (k.String() == "enter") && m.aiCommentCursor < len(m.aiCommentActivity):
		m.status = ""
		m.screen = screenAICommentDetail
		return m, nil
	default:
		return m.goMenu(), nil
	}
}

// regenCommentMsg carries the result of an async single-comment regeneration.
type regenCommentMsg struct {
	player string
	text   string
	err    error
}

// regenCommentCmd regenerates one player's comment off the UI thread (the model
// call takes seconds), delivering the result as a regenCommentMsg. extra is the
// optional one-off steering prompt the admin typed (empty ⇒ a plain regen).
func (m Model) regenCommentCmd(player, extra string) tea.Cmd {
	svc, user := m.svc, m.user
	return func() tea.Msg {
		txt, err := svc.RegenerateComment(user, player, extra)
		return regenCommentMsg{player: player, text: txt, err: err}
	}
}

// updateAICommentDetail handles the full-text comment view: "r" opens a steering
// box before regenerating THIS player's comment (async, leaving every other line
// untouched), "q" quits, any other key returns to the panel (Comments tab).
func (m Model) updateAICommentDetail(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case regenCommentMsg:
		if msg.err != nil {
			m.setStatus("regen falhou: "+msg.err.Error(), true)
			return m, nil
		}
		// Reflect the fresh text in the open detail view (and the feed entry behind it).
		if m.aiCommentCursor >= 0 && m.aiCommentCursor < len(m.aiCommentActivity) {
			m.aiCommentActivity[m.aiCommentCursor].Text = msg.text
			m.aiCommentActivity[m.aiCommentCursor].At = m.svc.Now()
			m.aiCommentActivity[m.aiCommentCursor].Outcome = "written"
		}
		m.setStatus("comentário de "+msg.player+" regenerado", false)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "r":
			if m.aiCommentCursor < 0 || m.aiCommentCursor >= len(m.aiCommentActivity) {
				return m, nil
			}
			player := m.aiCommentActivity[m.aiCommentCursor].Player
			if player == "" {
				m.setStatus("nada pra regenerar aqui", true)
				return m, nil
			}
			return m.openCommentRegen(player)
		default:
			m.betaniaTab = tabComments
			m.screen = screenBETanIA
			return m, nil
		}
	}
	return m, nil
}

// loadBETanIAComments loads the comment worker's status, recent feed and active
// tone. Used on screen-enter; tolerant of the worker being off (sets the flag).
func (m *Model) loadBETanIAComments() {
	_, cerr := m.svc.AICommentStatus(m.user)
	m.aiCommentsDisabled = errors.Is(cerr, service.ErrAIOff)
	if !m.aiCommentsDisabled {
		m.aiCommentStatus, _ = m.svc.AICommentStatus(m.user)
		m.aiCommentActivity, _ = m.svc.AICommentActivity(m.user, betaniaCommentLimit)
	}
	m.commentTone, _ = m.svc.CommentTone()
	// Keep the Comments-tab selection in range as the feed grows/shrinks.
	if m.aiCommentCursor >= len(m.aiCommentActivity) {
		m.aiCommentCursor = len(m.aiCommentActivity) - 1
	}
	if m.aiCommentCursor < 0 {
		m.aiCommentCursor = 0
	}
}

// refreshBETanIA reloads both the betting and comment panels after a key action.
func (m *Model) refreshBETanIA() {
	if st, err := m.svc.AIStatus(m.user); err == nil {
		m.aiStatus = st
		m.aiActivity, _ = m.svc.AIActivity(m.user, betaniaActivityLimit)
	}
	m.aiUsage, _ = m.svc.AIUsage(m.user)
	m.loadBETanIAComments()
}

// viewBETanIA renders the admin panel for the AI player: a status block (model,
// schedule, totals) and a feed of recent live picks with their rationale.
func (m Model) viewBETanIA() string {
	out := titleStyle.Render("⚙  Admin: BETanIA 🤖") + "\n\n"
	if m.aiDisabled {
		out += labelStyle.Render("BETanIA is not running.") + "\n"
		out += helpStyle.Render("Onboard once with `bethoven ai-seed`, then set BETHOVEN_AI_ENABLED=true") + "\n"
		out += helpStyle.Render("and BETHOVEN_AI_MODEL / ANTHROPIC_API_KEY, and restart.") + "\n\n"
		out += helpStyle.Render("any key: back · q: quit")
		return out
	}

	out += m.betaniaTabBar()
	switch m.betaniaTab {
	case tabComments:
		out += m.betaniaCommentsTab()
		out += "\n" + helpStyle.Render("comments → ai_comments.log — `tail -f` to watch") + "\n"
		out += statusLine(m) +
			helpStyle.Render("↑↓: select · enter: full text · c: regen all · t: tone · u: per-player · x: context · s: prompt") + "\n" +
			helpStyle.Render("tab: usage · q: quit · other: back")
		return out
	case tabUsage:
		out += m.betaniaUsageTab()
		out += "\n" + helpStyle.Render("usage → ai_usage.log — the durable cost record (survives restarts)") + "\n"
		out += statusLine(m) +
			helpStyle.Render("tab: betting · any other key: back · q: quit")
		return out
	}

	out += m.betaniaBettingTab()
	out += "\n" + helpStyle.Render("picks → "+aiLogHint()+" — `tail -f` to watch") + "\n"
	out += statusLine(m) +
		helpStyle.Render("↑↓/jk: scroll · r: run a betting pass now") + "\n" +
		helpStyle.Render("tab: comments · any other key: back · q: quit")
	return out
}

// betaniaTabBar renders the tab selector, highlighting the active tab.
func (m Model) betaniaTabBar() string {
	tab := func(label string, idx int) string {
		if m.betaniaTab == idx {
			return cursorOn.Render(" " + label + " ")
		}
		return helpStyle.Render(" " + label + " ")
	}
	return tab("Betting", tabBetting) + " " + tab("Comments", tabComments) + " " + tab("Usage", tabUsage) + "\n\n"
}

// betaniaUsageTab renders BETanIA's Claude token usage and estimated cost, broken
// down by category (bets / comments / live commentary) with a grand total. The
// figures come from the durable on-disk usage log, so they persist across restarts
// — unlike the "since restart" counters on the Betting tab.
func (m Model) betaniaUsageTab() string {
	now := m.svc.Now()
	rep := m.aiUsage

	if rep.Total.Calls == 0 {
		out := labelStyle.Render("Token usage & estimated cost") + "\n"
		out += helpStyle.Render("  no usage recorded yet — it accrues as BETanIA bets and comments") + "\n"
		return out
	}

	section := func(label string, c ai.CategoryUsage) string {
		s := labelStyle.Render(label) + "\n"
		s += kpi("Estimated cost", fmt.Sprintf("$%.2f", c.EstCostUSD))
		s += kpi("Calls", commaInt(c.Calls))
		s += kpi("Input tokens", commaInt(c.InputTokens))
		s += kpi("Output tokens", commaInt(c.OutputTokens))
		if c.WebSearches > 0 {
			s += kpi("Web searches", commaInt(c.WebSearches))
		}
		s += kpi("Avg latency", fmt.Sprintf("%d ms", c.AvgLatencyMS))
		if !c.LastAt.IsZero() {
			s += kpi("Last call", relativeAgo(now, c.LastAt))
		}
		return s
	}

	out := ""
	for _, c := range rep.Categories {
		out += section(usageCategoryLabel(c.Category), c) + "\n"
	}

	// Grand total — the headline numbers.
	out += labelStyle.Render("Total") + "\n"
	out += kpi("Estimated cost", okStyle.Render(fmt.Sprintf("$%.2f", rep.Total.EstCostUSD)))
	out += kpi("Tokens (in + out)", commaInt(rep.Total.InputTokens+rep.Total.OutputTokens))
	out += kpi("Calls", commaInt(rep.Total.Calls))
	out += "\n"

	out += helpStyle.Render("  Estimated from the on-disk usage log; persists across restarts. Prices are approximate.") + "\n"
	if len(rep.UnknownModels) > 0 {
		out += helpStyle.Render("  Cost under-counts unknown model(s): "+strings.Join(rep.UnknownModels, ", ")) + "\n"
	}
	return out
}

// usageCategoryLabel maps a usage category key to its panel heading.
func usageCategoryLabel(key string) string {
	switch key {
	case "bet":
		return "Bets"
	case "comment":
		return "Comments"
	case "live":
		return "Live commentary"
	case "digest":
		return "Derived notes"
	default:
		return key
	}
}

// commaInt formats an integer with thousands separators (e.g. 1234567 → "1,234,567").
func commaInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// betaniaBettingTab renders the live-worker status block and BETanIA's picks on
// record. Picks come from the DB (durable across restarts); where this session's
// in-memory feed has the rationale for a pick, it's shown beneath the row.
func (m Model) betaniaBettingTab() string {
	now := m.svc.Now()
	st := m.aiStatus

	out := labelStyle.Render("Status") + "\n"
	out += kpi("Model", st.Model)
	out += kpi("Betting every", st.Interval.String())
	out += kpi("Last run", relativeAgo(now, st.LastRun))
	out += kpi("Next run", untilText(now, st.NextRun))
	out += kpi("Bets placed (since restart)", fmt.Sprintf("%d", st.Placed))
	out += kpi("Skipped (already bet / not open)", fmt.Sprintf("%d", st.Skipped))
	out += kpi("Locked at kickoff", fmt.Sprintf("%d", st.Locked))
	out += kpi("Errors", fmt.Sprintf("%d", st.Errored))
	out += "\n"

	out += labelStyle.Render(fmt.Sprintf("Picks on record (%d)", len(m.aiBets))) + "\n"
	if len(m.aiBets) == 0 {
		out += helpStyle.Render("  nothing yet — the next pass will research and bet upcoming matches") + "\n"
		return out
	}
	// This session's rationale, keyed by "A vs B", to enrich the durable rows.
	rationale := make(map[string]ai.Action, len(m.aiActivity))
	for _, a := range m.aiActivity {
		rationale[a.Match] = a
	}
	rows := make([]string, len(m.aiBets))
	for i, ab := range m.aiBets {
		match := ab.Match.TeamA + " vs " + ab.Match.TeamB
		pick := ab.Bet // address a local copy for fmtPick
		var mark, tail string
		switch {
		case ab.Match.Live:
			mark = liveStyle.Render("⚡")
			tail = liveScore(ab.Match)
		case ab.Match.Finished:
			mark = okStyle.Render("✓")
			tail = fmt.Sprintf("→ %-5s %d pts", fmtResult(ab.Match), ab.Points)
		default:
			mark = okStyle.Render("·")
			tail = helpStyle.Render("upcoming")
		}
		row := fmt.Sprintf("  %-7s %s %-22s %-5s ",
			relativeAgo(now, ab.Bet.UpdatedAt), mark, truncate(match, 22), fmtPick(&pick)) + tail
		if a, ok := rationale[match]; ok && a.Rationale != "" {
			row += "\n" + helpStyle.Render("      "+truncate(a.Rationale, 78))
		}
		rows[i] = row
	}
	for _, r := range windowRows(rows, m.aiBetsCursor, m.bettingFeedCapacity()) {
		out += r + "\n"
	}
	return out
}

// betaniaCommentsTab renders the comment worker's status block and recent feed.
func (m Model) betaniaCommentsTab() string {
	now := m.svc.Now()
	if m.aiCommentsDisabled {
		return helpStyle.Render("  comment worker not running (set BETHOVEN_AI_COMMENTS_ENABLED=true)") + "\n"
	}
	cst := m.aiCommentStatus
	out := labelStyle.Render("Status") + "\n"
	out += kpi("Tone", m.commentTone)
	out += kpi("Regenerating every", cst.Interval.String())
	out += kpi("Last run", relativeAgo(now, cst.LastRun))
	out += kpi("Next run", untilText(now, cst.NextRun))
	out += kpi("Comments written", fmt.Sprintf("%d", cst.Written))
	out += kpi("Errors", fmt.Sprintf("%d", cst.Errored))
	out += "\n" + labelStyle.Render("Recent comments") + helpStyle.Render("  (↑↓ select · enter: full text)") + "\n"
	if len(m.aiCommentActivity) == 0 {
		out += helpStyle.Render("  nothing yet — press c to generate now") + "\n"
		return out
	}
	// Each comment is a two-line block (header + preview). Build them as single
	// items so windowRows can scroll the feed around the cursor — without this the
	// full field overflows the terminal and the selection slides off the bottom.
	rows := make([]string, len(m.aiCommentActivity))
	for i, a := range m.aiCommentActivity {
		cursor := "  "
		if i == m.aiCommentCursor {
			cursor = cursorOn.Render("▸ ")
		}
		head := fmt.Sprintf("%-8s %s %-16s", relativeAgo(now, a.At), outcomeMark(a.Outcome), truncate(a.Player, 16))
		if i == m.aiCommentCursor {
			head = cursorOn.Render(head)
		}
		row := cursor + head
		detail := a.Text
		if a.Outcome == "error" && a.Err != "" {
			detail = a.Err
		}
		if detail != "" {
			row += "\n" + helpStyle.Render("      "+truncate(detail, 78))
		}
		rows[i] = row
	}
	for _, r := range windowRows(rows, m.aiCommentCursor, m.commentFeedCapacity()) {
		out += r + "\n"
	}
	return out
}

// commentFeedCapacity is how many comment blocks fit on the Comments tab given the
// bettingFeedCapacity is how many pick rows fit on the Betting tab. Chrome is
// title (2) + tab bar (2) + status block (9 KPIs + label + blank = 11) + picks
// label (1) + footer (3) ≈ 19 lines. Floors at 3; falls back to 8 before resize.
func (m Model) bettingFeedCapacity() int {
	if m.height <= 0 {
		return 8
	}
	const bettingChrome = 19
	if n := m.height - bettingChrome; n >= 3 {
		return n
	}
	return 3
}

// terminal height. Each block is up to two lines (header + preview), and the tab's
// fixed chrome (title, tab bar, status block, labels, footer) is ~19 lines. Floors
// at 3 so the feed is always usable; falls back to 8 before the first size message.
func (m Model) commentFeedCapacity() int {
	if m.height <= 0 {
		return 8
	}
	const commentsChrome = 19
	if items := (m.height - commentsChrome) / 2; items >= 3 {
		return items
	}
	return 3
}

// viewAICommentDetail shows the full text of the comment selected in the Comments
// tab — the player it's about, when it was written, its outcome, and the wrapped
// untruncated line.
func (m Model) viewAICommentDetail() string {
	out := titleStyle.Render("⚙  BETanIA comment") + "\n\n"
	if m.aiCommentCursor < 0 || m.aiCommentCursor >= len(m.aiCommentActivity) {
		out += helpStyle.Render("  (no comment selected)") + "\n\n"
		out += helpStyle.Render("any key: back · q: quit")
		return out
	}
	a := m.aiCommentActivity[m.aiCommentCursor]
	out += kpi("Player", a.Player)
	out += kpi("Written", relativeAgo(m.svc.Now(), a.At))
	out += kpi("Outcome", a.Outcome)
	out += "\n"
	text := a.Text
	if a.Outcome == "error" && a.Err != "" {
		text = "error: " + a.Err
	}
	width := m.width - 4
	if width < 20 {
		width = 76
	}
	for _, ln := range wrapText(text, width) {
		out += "  " + commentStyle.Render(ln) + "\n"
	}
	out += "\n" + statusLine(m)
	out += helpStyle.Render("r: regenerate (optional steering) · q: quit · other: back to Comments")
	return out
}

// outcomeMark is a compact glyph for an action's outcome.
func outcomeMark(outcome string) string {
	switch outcome {
	case "placed", "written":
		return okStyle.Render("✓")
	case "locked":
		return helpStyle.Render("⏱")
	case "error":
		return errStyle.Render("✗")
	default:
		return " "
	}
}

// untilText formats a future time as "in 34m" / "in 2h57m"; past/zero degrade gracefully.
func untilText(now, t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := t.Sub(now)
	switch {
	case d <= 0:
		return "due now"
	case d < time.Hour:
		return fmt.Sprintf("in %dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("in %dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// aiLogHint names the on-disk log so the admin knows where the durable history is.
func aiLogHint() string { return "ai_bets.log" }
