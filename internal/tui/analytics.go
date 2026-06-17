package tui

import (
	"fmt"
	"strings"
	"time"

	"bethoven/internal/analytics"
)

const (
	// analyticsTimelineDays is the window for the accesses-per-day histogram.
	analyticsTimelineDays = 14
	// analyticsRecentLimit caps the recent-activity feed.
	analyticsRecentLimit = 12
	// analyticsTopPlayers caps the per-player table.
	analyticsTopPlayers = 10
)

// sparkBlocks renders bar heights for the activity histogram, low→high.
var sparkBlocks = []rune("▁▂▃▄▅▆▇█")

// viewAnalytics renders the admin usage panel: KPIs, an accesses-per-day
// histogram, a per-player engagement table, and a recent-activity feed.
func (m Model) viewAnalytics() string {
	out := titleStyle.Render("⚙  Admin: analytics") + "\n\n"
	if m.anDisabled {
		out += labelStyle.Render("Analytics is disabled.") + "\n"
		out += helpStyle.Render("Set BETHOVEN_ANALYTICS_ENABLED=true and restart to start recording.") + "\n\n"
		out += helpStyle.Render("any key: back · q: quit")
		return out
	}

	now := m.svc.Now()
	ov := m.anOverview

	// --- Overview KPIs ---
	out += labelStyle.Render("Overview") + "\n"
	out += kpi("Accesses (total)", fmt.Sprintf("%d", ov.TotalAccesses))
	out += kpi("Accesses today", fmt.Sprintf("%d", ov.AccessesToday))
	out += kpi("Accesses (7d)", fmt.Sprintf("%d", ov.Accesses7d))
	out += kpi("Unique players", fmt.Sprintf("%d", ov.UniquePlayers))
	out += kpi("Active (7d) / registered", fmt.Sprintf("%d / %d", ov.ActivePlayers, ov.RegisteredPlayers))
	out += kpi("Bets placed", fmt.Sprintf("%d", ov.BetsPlaced))
	out += "\n"

	// --- Activity over time ---
	out += labelStyle.Render(fmt.Sprintf("Accesses / day (last %d)", analyticsTimelineDays)) + "\n"
	out += renderSparkline(m.anTimeline) + "\n\n"

	// --- Per-player breakdown ---
	out += labelStyle.Render("Per player") + "\n"
	if len(m.anPlayers) == 0 {
		out += helpStyle.Render("  no activity yet") + "\n"
	} else {
		out += helpStyle.Render(fmt.Sprintf("  %-20s %8s %8s  %s", "player", "access", "bets", "last seen")) + "\n"
		for i, p := range m.anPlayers {
			if i >= analyticsTopPlayers {
				out += helpStyle.Render(fmt.Sprintf("  …and %d more", len(m.anPlayers)-analyticsTopPlayers)) + "\n"
				break
			}
			actor := p.Actor
			if actor == "" {
				actor = "(unknown)"
			}
			out += fmt.Sprintf("  %-20s %8d %8d  %s\n",
				truncate(actor, 20), p.Accesses, p.Bets, relativeAgo(now, p.LastSeen))
		}
	}
	out += "\n"

	// --- Recent activity feed ---
	out += labelStyle.Render("Recent activity") + "\n"
	if len(m.anRecent) == 0 {
		out += helpStyle.Render("  nothing recorded yet") + "\n"
	} else {
		for _, ev := range m.anRecent {
			actor := ev.Actor
			if actor == "" {
				actor = "(unknown)"
			}
			line := fmt.Sprintf("  %-8s %-14s %-16s %s",
				relativeAgo(now, ev.At), truncate(actor, 14), ev.Name, eventDetail(ev))
			out += helpStyle.Render(strings.TrimRight(line, " ")) + "\n"
		}
	}

	out += "\n" + statusLine(m) + helpStyle.Render("any key: back · q: quit")
	return out
}

// kpi renders one aligned key/value line of the overview block.
func kpi(label, value string) string {
	return "  " + labelStyle.Render(fmt.Sprintf("%-26s", label)) + okStyle.Render(value) + "\n"
}

// renderSparkline draws a one-line bar chart of the daily access buckets. The
// tallest day maps to the fullest block; empty input shows a dim placeholder.
func renderSparkline(buckets []analytics.Bucket) string {
	if len(buckets) == 0 {
		return helpStyle.Render("  (no accesses yet)")
	}
	max := 0
	for _, b := range buckets {
		if b.Count > max {
			max = b.Count
		}
	}
	var bars strings.Builder
	for _, b := range buckets {
		idx := 0
		if max > 0 {
			idx = (b.Count * (len(sparkBlocks) - 1)) / max
		}
		bars.WriteRune(sparkBlocks[idx])
	}
	// Label the span with the first/last day and the peak count.
	span := buckets[0].Day
	if len(buckets) > 1 {
		span += " → " + buckets[len(buckets)-1].Day
	}
	return "  " + liveStyle.Render(bars.String()) + "   " +
		helpStyle.Render(fmt.Sprintf("%s  (peak %d/day)", span, max))
}

// eventDetail renders a compact, human-readable summary of an event's props.
func eventDetail(ev analytics.Event) string {
	switch ev.Name {
	case "view":
		return ev.Props["screen"]
	case "bet_placed":
		s := ev.Props["match"] + " " + ev.Props["pred"]
		if ev.Props["update"] == "true" {
			s += " (edit)"
		}
		return s
	case "result_entered":
		return "match " + ev.Props["match_id"] + " → " + ev.Props["score"]
	case "match_added":
		return ev.Props["match"] + " " + ev.Props["phase"]
	case "setting_changed":
		return ev.Props["setting"] + "=" + ev.Props["value"]
	case "session_start":
		if ev.Props["known"] == "false" {
			return "new key"
		}
		return ""
	default:
		return ""
	}
}

// relativeAgo formats a timestamp as a short "time ago" string relative to now.
func relativeAgo(now, t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// truncate clamps s to n runes, adding an ellipsis when it overflows.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
