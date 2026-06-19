package tui

import (
	"errors"
	"fmt"
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

// BETanIA admin tabs.
const (
	tabBetting  = 0
	tabComments = 1
)

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
	case k.String() == "tab" || k.String() == "right" || k.String() == "left" || k.String() == "shift+tab":
		// Two tabs only — any switch key just flips between them.
		if m.betaniaTab == tabBetting {
			m.betaniaTab = tabComments
		} else {
			m.betaniaTab = tabBetting
		}
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
	default:
		return m.goMenu(), nil
	}
}

// loadBETanIAComments loads the comment worker's status, recent feed and active
// tone. Used on screen-enter; tolerant of the worker being off (sets the flag).
func (m *Model) loadBETanIAComments() {
	_, cerr := m.svc.AICommentStatus(m.user)
	m.aiCommentsDisabled = errors.Is(cerr, service.ErrAIOff)
	if !m.aiCommentsDisabled {
		m.aiCommentStatus, _ = m.svc.AICommentStatus(m.user)
		m.aiCommentActivity, _ = m.svc.AICommentActivity(m.user, betaniaActivityLimit)
	}
	m.commentTone, _ = m.svc.CommentTone()
}

// refreshBETanIA reloads both the betting and comment panels after a key action.
func (m *Model) refreshBETanIA() {
	if st, err := m.svc.AIStatus(m.user); err == nil {
		m.aiStatus = st
		m.aiActivity, _ = m.svc.AIActivity(m.user, betaniaActivityLimit)
	}
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
	if m.betaniaTab == tabComments {
		out += m.betaniaCommentsTab()
		out += "\n" + helpStyle.Render("comments → ai_comments.log — `tail -f` to watch") + "\n"
		out += statusLine(m) +
			helpStyle.Render("c: regen all · t: default tone · u: per-player tone · x: context") + "\n" +
			helpStyle.Render("tab: betting · any other key: back · q: quit")
		return out
	}

	out += m.betaniaBettingTab()
	out += "\n" + helpStyle.Render("picks → "+aiLogHint()+" — `tail -f` to watch") + "\n"
	out += statusLine(m) +
		helpStyle.Render("r: run a betting pass now") + "\n" +
		helpStyle.Render("tab: comments · any other key: back · q: quit")
	return out
}

// betaniaTabBar renders the two-tab selector, highlighting the active tab.
func (m Model) betaniaTabBar() string {
	tab := func(label string, idx int) string {
		if m.betaniaTab == idx {
			return cursorOn.Render(" " + label + " ")
		}
		return helpStyle.Render(" " + label + " ")
	}
	return tab("Betting", tabBetting) + " " + tab("Comments", tabComments) + "\n\n"
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
	for _, ab := range m.aiBets {
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
		out += fmt.Sprintf("  %-7s %s %-22s %-5s ",
			relativeAgo(now, ab.Bet.UpdatedAt), mark, truncate(match, 22), fmtPick(&pick)) + tail + "\n"
		if a, ok := rationale[match]; ok && a.Rationale != "" {
			out += helpStyle.Render("      "+truncate(a.Rationale, 78)) + "\n"
		}
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
	out += "\n" + labelStyle.Render("Recent comments") + "\n"
	if len(m.aiCommentActivity) == 0 {
		out += helpStyle.Render("  nothing yet — press c to generate now") + "\n"
		return out
	}
	for _, a := range m.aiCommentActivity {
		out += fmt.Sprintf("  %-8s %s %-16s\n",
			relativeAgo(now, a.At), outcomeMark(a.Outcome), truncate(a.Player, 16))
		detail := a.Text
		if a.Outcome == "error" && a.Err != "" {
			detail = a.Err
		}
		if detail != "" {
			out += helpStyle.Render("      "+truncate(detail, 78)) + "\n"
		}
	}
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
