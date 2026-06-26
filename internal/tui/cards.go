package tui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/service"
)

// --- player-card list (screenPlayerCards) ---------------------------------------

// generateCardsMsg carries the result of the async "generate all cards" pass (one
// Claude call per player, so it runs off the UI thread).
type generateCardsMsg struct{ err error }

// generateCardsCmd regenerates every player's card narrative off the UI thread.
func (m Model) generateCardsCmd() tea.Cmd {
	svc, user := m.svc, m.user
	return func() tea.Msg { return generateCardsMsg{err: svc.GeneratePlayerCards(user)} }
}

func (m Model) updatePlayerCards(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case generateCardsMsg:
		m.cardBusy = false
		if msg.err != nil {
			m.setStatus("some cards failed: "+msg.err.Error(), true)
		} else {
			m.setStatus("player cards generated", false)
		}
		// Reload so the freshly persisted narratives show on the list + in detail.
		if cards, err := m.svc.PlayerCards(m.user); err == nil {
			m.cards = cards
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "esc", "b":
			return m.goMenu(), nil
		case "up", "k":
			if m.cardCursor > 0 {
				m.cardCursor--
			}
		case "down", "j":
			if m.cardCursor < len(m.cards)-1 {
				m.cardCursor++
			}
		case "enter":
			if len(m.cards) > 0 {
				m.cardSel = m.cardCursor
				m.status = ""
				m.screen = screenPlayerCard
			}
		case "g":
			if m.cardBusy {
				return m, nil
			}
			if len(m.cards) == 0 {
				m.setStatus("no finished matches yet", true)
				return m, nil
			}
			m.cardBusy = true
			m.setStatus("generating cards… (one Claude call per player; this can take a minute)", false)
			return m, m.generateCardsCmd()
		}
	}
	return m, nil
}

func (m Model) viewPlayerCards() string {
	out := titleStyle.Render("🏆  Player cards") + "\n"
	out += helpStyle.Render("Final standings — admin preview (players can't see these yet)") + "\n\n"
	if len(m.cards) == 0 {
		out += labelStyle.Render("No finished matches yet — cards appear once results are in.") + "\n\n"
		return out + statusLine(m) + helpStyle.Render("b: back · q: quit")
	}
	for i, c := range m.cards {
		rc := fmt.Sprintf("%2d.", c.FinalRank)
		if e := medalEmoji(c.Medal); e != "" {
			rc = e
		}
		label := fmt.Sprintf("%-3s %-22s %4d pts", rc, truncate(c.User.DisplayName, 22), c.Total)
		suffix := "   " + helpStyle.Render("— not generated")
		if c.Narrative != "" {
			suffix = "   " + okStyle.Render("🤖 ✓")
		}
		if i == m.cardCursor {
			out += selBar.Render("▸ "+label) + suffix + "\n"
		} else {
			out += "  " + labelStyle.Render(label) + suffix + "\n"
		}
	}
	out += "\n" + statusLine(m)
	out += helpStyle.Render("g: generate all · enter: open · ↑/↓: move · b: back · q: quit")
	return out
}

// --- single player card (screenPlayerCard) --------------------------------------

// regenCardMsg carries the result of regenerating ONE player's card narrative.
type regenCardMsg struct {
	text string
	err  error
}

func (m Model) regenCardCmd(userID int64) tea.Cmd {
	svc, user := m.svc, m.user
	return func() tea.Msg {
		txt, err := svc.RegeneratePlayerCard(user, userID)
		return regenCardMsg{text: txt, err: err}
	}
}

func (m Model) updatePlayerCard(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case regenCardMsg:
		m.cardBusy = false
		if msg.err != nil {
			m.setStatus("regen failed: "+msg.err.Error(), true)
			return m, nil
		}
		if m.cardSel >= 0 && m.cardSel < len(m.cards) {
			m.cards[m.cardSel].Narrative = msg.text
			m.cards[m.cardSel].NarratedAt = m.svc.Now()
		}
		m.setStatus("card regenerated", false)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "esc", "b":
			m.status, m.screen = "", screenPlayerCards
			return m, nil
		case "left", "h", "p":
			if n := len(m.cards); n > 0 {
				m.cardSel = (m.cardSel - 1 + n) % n
				m.cardCursor = m.cardSel // keep the list cursor in sync for when we go back
				m.status = ""
			}
			return m, nil
		case "right", "l", "n", " ":
			if n := len(m.cards); n > 0 {
				m.cardSel = (m.cardSel + 1) % n
				m.cardCursor = m.cardSel
				m.status = ""
			}
			return m, nil
		case "r":
			if m.cardBusy || m.cardSel < 0 || m.cardSel >= len(m.cards) {
				return m, nil
			}
			m.cardBusy = true
			m.setStatus("regenerating…", false)
			return m, m.regenCardCmd(m.cards[m.cardSel].User.ID)
		}
	}
	return m, nil
}

func (m Model) viewPlayerCard() string {
	if m.cardSel < 0 || m.cardSel >= len(m.cards) {
		return "no card\n"
	}
	c := m.cards[m.cardSel]
	col := cardBorderColor(c.Medal)

	var b strings.Builder
	head := c.User.DisplayName + " — " + cardPlace(c.FinalRank)
	if e := medalEmoji(c.Medal); e != "" {
		head = e + "  " + head
	}
	if c.IsSelf {
		head += "  (me)"
	}
	b.WriteString(lipgloss.NewStyle().Foreground(col).Bold(true).Render(head) + "\n\n")

	// Trajectory: a rank sparkline (inverted so the leader's bar is tallest) + arc.
	vals := make([]int, len(c.Trajectory))
	for i, t := range c.Trajectory {
		vals[i] = -t.Position
	}
	arc := fmt.Sprintf("%s → %s", cardOrdinal(c.StartRank), cardOrdinal(c.FinalRank))
	if c.PeakRank > 0 {
		arc += fmt.Sprintf(", peak %s", cardOrdinal(c.PeakRank))
	}
	b.WriteString(labelStyle.Render("Trajectory ") + liveStyle.Render(sparkline(vals)) + "  " + helpStyle.Render(arc) + "\n")
	if c.BiggestClimb > 0 {
		places := "places"
		if c.BiggestClimb == 1 {
			places = "place"
		}
		b.WriteString(helpStyle.Render(fmt.Sprintf("Best round  +%d %s (%s)", c.BiggestClimb, places, c.ClimbRound)) + "\n")
	}
	b.WriteString(labelStyle.Render(fmt.Sprintf("%d pts · %d exact · %d correct", c.Total, c.ExactHits, c.CorrectResults)) + "\n")

	// Accuracy & momentum — only the parts that say something for this player.
	stats := []string{fmt.Sprintf("bet %d/%d", c.MatchesBet, c.MatchesAvailable)}
	if c.MatchesBet > 0 {
		stats = append(stats, fmt.Sprintf("%d%% hit rate", c.HitRate))
	}
	if c.BestStreak >= 2 {
		stats = append(stats, fmt.Sprintf("streak %d", c.BestStreak))
	}
	if c.RoundsAsLeader > 0 {
		round := "round"
		if c.RoundsAsLeader > 1 {
			round = "rounds"
		}
		stats = append(stats, fmt.Sprintf("%d %s at #1", c.RoundsAsLeader, round))
	}
	b.WriteString(helpStyle.Render(strings.Join(stats, " · ")) + "\n")

	// Tenure flags — late joiner or a give-up tail, only when they apply.
	var notes []string
	if c.JoinedLate {
		notes = append(notes, fmt.Sprintf("joined late (missed %d before)", c.MatchesBeforeJoining))
	}
	if c.RecentSkips > 0 {
		notes = append(notes, fmt.Sprintf("went quiet (last %d blank)", c.RecentSkips))
	}
	if len(notes) > 0 {
		b.WriteString(helpStyle.Render(strings.Join(notes, " · ")) + "\n")
	}

	if c.BestPick != nil {
		b.WriteString("\n" + okStyle.Render("Best call  ") + cardPickText(c.BestPick) + "\n")
	}
	if c.WorstPick != nil {
		b.WriteString(errStyle.Render("Worst miss ") + cardPickText(c.WorstPick) + "\n")
	}

	b.WriteString("\n")
	if c.Narrative != "" {
		b.WriteString(botMark.Render("🤖") + " " + commentStyle.Render(splitParagraphs(c.Narrative, 1)))
	} else {
		b.WriteString(helpStyle.Render("No narrative yet — press r to generate this card."))
	}

	cardW := 60
	if m.width > 0 && m.width-6 < cardW {
		cardW = m.width - 6
	}
	if cardW < 30 {
		cardW = 30
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(col).
		Padding(0, 1).
		Width(cardW)

	out := box.Render(b.String()) + "\n\n"
	pos := helpStyle.Render(fmt.Sprintf("card %d/%d  ", m.cardSel+1, len(m.cards)))
	return out + statusLine(m) + pos + helpStyle.Render("←/→: prev/next · r: regenerate · esc: back · q: quit")
}

// --- helpers --------------------------------------------------------------------

// cardPickText renders a best-call / worst-miss row: the real result, the pick, and
// the points. Caller guarantees mr and mr.Bet are non-nil.
func cardPickText(mr *service.MatchResult) string {
	m := mr.Match
	actual := "?"
	if m.ScoreA != nil && m.ScoreB != nil {
		actual = fmt.Sprintf("%d-%d", *m.ScoreA, *m.ScoreB)
	}
	pred := fmt.Sprintf("%d-%d", mr.Bet.PredA, mr.Bet.PredB)
	if mr.Points > 0 {
		return fmt.Sprintf("%s %s %s  (you said %s, +%d)", m.TeamA, actual, m.TeamB, pred, mr.Points)
	}
	return fmt.Sprintf("%s %s %s  (you said %s)", m.TeamA, actual, m.TeamB, pred)
}

// medalEmoji returns the podium medal for a top-three finisher, else "".
func medalEmoji(medal int) string {
	switch medal {
	case 1:
		return "🥇"
	case 2:
		return "🥈"
	case 3:
		return "🥉"
	}
	return ""
}

// cardBorderColor tints a card by podium finish: gold/silver/bronze for the top
// three, dim for everyone else.
func cardBorderColor(medal int) lipgloss.Color {
	switch medal {
	case 1:
		return gold
	case 2:
		return silver
	case 3:
		return bronze
	}
	return dim
}

// cardPlace names a finishing position: Champion / Runner-up / Third place / Nth.
func cardPlace(rank int) string {
	switch rank {
	case 1:
		return "Champion"
	case 2:
		return "Runner-up"
	case 3:
		return "Third place"
	}
	return cardOrdinal(rank) + " place"
}

// cardOrdinal renders 1 as "1st", 2 as "2nd", … with the usual English rules.
func cardOrdinal(n int) string {
	if n%100 >= 11 && n%100 <= 13 {
		return fmt.Sprintf("%dth", n)
	}
	switch n % 10 {
	case 1:
		return fmt.Sprintf("%dst", n)
	case 2:
		return fmt.Sprintf("%dnd", n)
	case 3:
		return fmt.Sprintf("%drd", n)
	}
	return fmt.Sprintf("%dth", n)
}

// splitParagraphs regroups a long prose narrative into paragraphs of about perPara
// sentences each (separated by a blank line), so a card narrative reads as a few
// blocks instead of one wall of text. Sentence-aware: it breaks on . ! ? followed by
// a space and the start of a new sentence (uppercase / opening quote / digit), so
// mid-sentence tokens like "5-0" or odds "2.5" never trigger a break. Returns the
// text as one block when it's already short.
func splitParagraphs(text string, perPara int) string {
	text = strings.TrimSpace(text)
	if text == "" || perPara < 1 {
		return text
	}
	runes := []rune(text)
	var sentences []string
	start := 0
	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '.', '!', '?':
		default:
			continue
		}
		// Consume any closing quotes/brackets right after the terminator.
		j := i + 1
		for j < len(runes) && (runes[j] == '"' || runes[j] == '\'' || runes[j] == ')' || runes[j] == ']') {
			j++
		}
		if j >= len(runes) || runes[j] != ' ' {
			continue // end of text (flushed below) or a token like "2.5" — no break
		}
		// Peek the first non-space rune; only break before a genuine new sentence.
		k := j
		for k < len(runes) && runes[k] == ' ' {
			k++
		}
		if k >= len(runes) {
			break
		}
		if nr := runes[k]; !(unicode.IsUpper(nr) || unicode.IsDigit(nr) || nr == '"' || nr == '\'') {
			continue
		}
		sentences = append(sentences, strings.TrimSpace(string(runes[start:j])))
		start, i = k, k-1
	}
	if start < len(runes) {
		if tail := strings.TrimSpace(string(runes[start:])); tail != "" {
			sentences = append(sentences, tail)
		}
	}
	if len(sentences) <= perPara {
		return strings.Join(sentences, " ")
	}
	var paras []string
	for i := 0; i < len(sentences); i += perPara {
		end := i + perPara
		if end > len(sentences) {
			end = len(sentences)
		}
		paras = append(paras, strings.Join(sentences[i:end], " "))
	}
	return strings.Join(paras, "\n\n")
}

// sparkline renders a row of block glyphs, scaling the values so the largest is the
// tallest bar. Equal values render mid-height. Used for a player's rank arc (pass
// negated positions so the leader peaks).
func sparkline(vals []int) string {
	if len(vals) == 0 {
		return ""
	}
	bars := []rune("▁▂▃▄▅▆▇█")
	min, max := vals[0], vals[0]
	for _, v := range vals {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	var b strings.Builder
	for _, v := range vals {
		idx := len(bars) / 2
		if max > min {
			idx = (v - min) * (len(bars) - 1) / (max - min)
		}
		b.WriteRune(bars[idx])
	}
	return b.String()
}
