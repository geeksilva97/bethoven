package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/service"
)

// ctxMode drives the rivalry/context editor's small wizard.
const (
	ctxModeList      = iota // browsing rivalries + notes
	ctxModeNote             // typing a house note
	ctxModePickA            // picking the first player of a rivalry
	ctxModePickB            // picking the second player
	ctxModeRivalNote        // typing the rivalry note
)

// openBETanIA (re)loads the admin panel — used when returning from a sub-editor.
func (m Model) openBETanIA() Model {
	m.status = ""
	m.aiDisabled = false
	st, err := m.svc.AIStatus(m.user)
	if errors.Is(err, service.ErrAIOff) {
		m.aiDisabled, m.screen = true, screenBETanIA
		return m
	}
	if err != nil {
		m.setStatus(err.Error(), true)
		m.screen = screenBETanIA
		return m
	}
	m.aiStatus = st
	m.aiActivity, _ = m.svc.AIActivity(m.user, betaniaActivityLimit)
	m.aiBets, _ = m.svc.AIBets(m.user, betaniaBetsLimit)
	m.loadBETanIAComments()
	m.screen = screenBETanIA
	return m
}

// ---- per-player tone editor -------------------------------------------------

func (m Model) openAITones() Model {
	m.status = ""
	pts, err := m.svc.PlayerTones(m.user)
	if err != nil {
		m.setStatus(err.Error(), true)
		return m
	}
	m.tonePlayers = pts
	if m.toneCursor >= len(pts) {
		m.toneCursor = 0
	}
	m.commentTone, _ = m.svc.CommentTone()
	m.screen = screenAITones
	return m
}

func (m Model) updateAITones(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "esc", "b":
		return m.openBETanIA(), nil
	case "up", "k":
		if m.toneCursor > 0 {
			m.toneCursor--
		}
	case "down", "j":
		if m.toneCursor < len(m.tonePlayers)-1 {
			m.toneCursor++
		}
	case "right", "l", " ", "enter":
		return m.cycleTone(1), nil
	case "left", "h":
		return m.cycleTone(-1), nil
	}
	return m, nil
}

// cycleTone advances the selected player's tone through default→playful→savage→
// mute (dir +1) or backwards, persisting immediately.
func (m Model) cycleTone(dir int) Model {
	if len(m.tonePlayers) == 0 {
		return m
	}
	order := []string{"default", "playful", "savage", "mute"}
	cur := m.tonePlayers[m.toneCursor].Tone
	idx := 0
	for i, o := range order {
		if o == cur {
			idx = i
			break
		}
	}
	next := order[(idx+dir+len(order))%len(order)]
	pt := m.tonePlayers[m.toneCursor]
	if err := m.svc.SetUserCommentTone(m.user, pt.User.ID, next); err != nil {
		m.setStatus(err.Error(), true)
		return m
	}
	m.tonePlayers[m.toneCursor].Tone = next
	m.setStatus(pt.User.DisplayName+" → "+toneLabel(next), false)
	return m
}

func toneLabel(t string) string {
	switch t {
	case "playful":
		return "[playful]"
	case "savage":
		return "[savage]"
	case "mute":
		return "[muted — no comment]"
	default:
		return "[default]"
	}
}

func (m Model) viewAITones() string {
	out := titleStyle.Render("⚙  BETanIA: tone per player") + "\n\n"
	out += helpStyle.Render("global default: "+m.commentTone+"  ·  muted players get no comment at all") + "\n\n"
	if len(m.tonePlayers) == 0 {
		out += helpStyle.Render("  no players yet") + "\n"
	}
	for i, pt := range m.tonePlayers {
		line := fmt.Sprintf("%-22s %s", truncate(pt.User.DisplayName, 22), toneLabel(pt.Tone))
		out += cursorRow(i == m.toneCursor, line) + "\n"
	}
	out += "\n" + statusLine(m) + helpStyle.Render("↑/↓: player · ←/→ or space: change tone · esc: back · q: quit")
	return out
}

// ---- rivalry / house-note context editor ------------------------------------

func (m Model) openAIContext() Model {
	m.status = ""
	v, err := m.svc.CommentContextView(m.user)
	if err != nil {
		m.setStatus(err.Error(), true)
		return m
	}
	pts, err := m.svc.PlayerTones(m.user)
	if err != nil {
		m.setStatus(err.Error(), true)
		return m
	}
	m.ctxView, m.tonePlayers = v, pts
	m.ctxMode, m.ctxCursor = ctxModeList, 0
	m.screen = screenAIContext
	return m
}

func (m *Model) reloadCtx() {
	if v, err := m.svc.CommentContextView(m.user); err == nil {
		m.ctxView = v
	}
}

func (m Model) updateAIContext(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.ctxMode {
	case ctxModeNote, ctxModeRivalNote:
		return m.updateCtxInput(msg)
	case ctxModePickA, ctxModePickB:
		return m.updateCtxPick(msg)
	default:
		return m.updateCtxList(msg)
	}
}

func (m Model) updateCtxList(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	total := len(m.ctxView.Rivalries) + len(m.ctxView.Notes)
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "esc", "b":
		return m.openBETanIA(), nil
	case "up", "k":
		if m.ctxCursor > 0 {
			m.ctxCursor--
		}
	case "down", "j":
		if m.ctxCursor < total-1 {
			m.ctxCursor++
		}
	case "n":
		m.ctxMode = ctxModeNote
		m.ctxInput = newCtxInput("a house note about the pool…")
		return m, textinput.Blink
	case "a":
		if len(m.tonePlayers) < 2 {
			m.setStatus("need at least two players for a rivalry", true)
			return m, nil
		}
		m.ctxMode, m.ctxPickCursor = ctxModePickA, 0
		return m, nil
	case "d":
		if total == 0 {
			return m, nil
		}
		var err error
		if m.ctxCursor < len(m.ctxView.Rivalries) {
			err = m.svc.DeleteRivalry(m.user, m.ctxCursor)
		} else {
			err = m.svc.DeleteCommentNote(m.user, m.ctxCursor-len(m.ctxView.Rivalries))
		}
		if err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.reloadCtx()
		if n := len(m.ctxView.Rivalries) + len(m.ctxView.Notes); m.ctxCursor >= n && m.ctxCursor > 0 {
			m.ctxCursor = n - 1
		}
	}
	return m, nil
}

func (m Model) updateCtxInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.Type {
		case tea.KeyEsc:
			m.ctxMode = ctxModeList
			return m, nil
		case tea.KeyEnter:
			text := strings.TrimSpace(m.ctxInput.Value())
			if text == "" {
				m.setStatus("type something first (esc to cancel)", true)
				return m, nil
			}
			var err error
			if m.ctxMode == ctxModeRivalNote {
				err = m.svc.AddRivalry(m.user, m.ctxRivalA, m.ctxRivalB, text)
			} else {
				err = m.svc.AddCommentNote(m.user, text)
			}
			if err != nil {
				m.setStatus(err.Error(), true)
				return m, nil
			}
			m.reloadCtx()
			m.ctxMode = ctxModeList
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.ctxInput, cmd = m.ctxInput.Update(msg)
	return m, cmd
}

func (m Model) updateCtxPick(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "esc":
		m.ctxMode = ctxModeList
		return m, nil
	case "up", "k":
		if m.ctxPickCursor > 0 {
			m.ctxPickCursor--
		}
	case "down", "j":
		if m.ctxPickCursor < len(m.tonePlayers)-1 {
			m.ctxPickCursor++
		}
	case "enter":
		sel := m.tonePlayers[m.ctxPickCursor].User
		if m.ctxMode == ctxModePickA {
			m.ctxRivalA, m.ctxRivalAName = sel.ID, sel.DisplayName
			m.ctxMode, m.ctxPickCursor = ctxModePickB, 0
			return m, nil
		}
		if sel.ID == m.ctxRivalA {
			m.setStatus("pick a different player", true)
			return m, nil
		}
		m.ctxRivalB = sel.ID
		m.ctxMode = ctxModeRivalNote
		m.ctxInput = newCtxInput(fmt.Sprintf("why are %s and %s rivals?…", m.ctxRivalAName, sel.DisplayName))
		return m, textinput.Blink
	}
	return m, nil
}

func (m Model) viewAIContext() string {
	switch m.ctxMode {
	case ctxModeNote:
		return titleStyle.Render("⚙  BETanIA: add house note") + "\n\n" +
			m.ctxInput.View() + "\n\n" + helpStyle.Render("enter: save · esc: cancel")
	case ctxModeRivalNote:
		head := fmt.Sprintf("%s  vs  %s", m.ctxRivalAName, nameByID(m.tonePlayers, m.ctxRivalB))
		return titleStyle.Render("⚙  BETanIA: rivalry note") + "\n\n" +
			labelStyle.Render(head) + "\n\n" + m.ctxInput.View() + "\n\n" +
			helpStyle.Render("enter: save · esc: cancel")
	case ctxModePickA, ctxModePickB:
		return m.viewCtxPick()
	default:
		return m.viewCtxList()
	}
}

func (m Model) viewCtxList() string {
	out := titleStyle.Render("⚙  BETanIA: rivalries & context") + "\n\n"
	w := m.width - 6
	out += labelStyle.Render("Rivalries") + "\n"
	if len(m.ctxView.Rivalries) == 0 {
		out += helpStyle.Render("  none yet") + "\n"
	}
	idx := 0
	for _, r := range m.ctxView.Rivalries {
		out += cursorRow(idx == m.ctxCursor, truncate(fmt.Sprintf("%s vs %s — %s", r.A, r.B, r.Note), w)) + "\n"
		idx++
	}
	out += "\n" + labelStyle.Render("House notes") + "\n"
	if len(m.ctxView.Notes) == 0 {
		out += helpStyle.Render("  none yet") + "\n"
	}
	for _, n := range m.ctxView.Notes {
		out += cursorRow(idx == m.ctxCursor, truncate(n, w)) + "\n"
		idx++
	}
	out += "\n" + statusLine(m) +
		helpStyle.Render("a: add rivalry · n: add note · d: delete · ↑/↓: move · esc: back")
	return out
}

func (m Model) viewCtxPick() string {
	title := "pick the FIRST player"
	if m.ctxMode == ctxModePickB {
		title = fmt.Sprintf("pick the SECOND player (rival of %s)", m.ctxRivalAName)
	}
	out := titleStyle.Render("⚙  BETanIA: add rivalry") + "\n\n" + labelStyle.Render(title) + "\n\n"
	for i, pt := range m.tonePlayers {
		out += cursorRow(i == m.ctxPickCursor, pt.User.DisplayName) + "\n"
	}
	out += "\n" + statusLine(m) + helpStyle.Render("↑/↓: move · enter: pick · esc: cancel")
	return out
}

// ---- comment-prompt override editor -----------------------------------------

// openAIPrompt loads the current comment-prompt override into the editor. When no
// override is set, the box is pre-filled with the built-in default so the admin can
// edit the real prompt instead of starting blank; saving it unchanged resets back
// to the (dynamic) built-in. An empty value means BETanIA uses her built-in prompt.
func (m Model) openAIPrompt() Model {
	m.status = ""
	cur, err := m.svc.CommentPromptOverride()
	if err != nil {
		m.setStatus(err.Error(), true)
		return m
	}
	if strings.TrimSpace(cur) == "" {
		cur = m.svc.DefaultCommentPrompt()
	}
	ta := textarea.New()
	ta.Placeholder = "(empty — using built-in default)"
	ta.CharLimit = 4000
	ta.SetWidth(72)
	ta.SetHeight(12)
	ta.SetValue(cur)
	ta.CursorEnd()
	ta.Focus()
	m.promptInput = ta
	m.screen = screenAIPrompt
	return m
}

// updateAIPrompt edits the override: ctrl+s saves, esc cancels (enter inserts a
// newline — the override is a multi-line prompt body). A blank value restores the
// built-in default. Returns to the Comments tab on save/cancel.
func (m Model) updateAIPrompt(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.Type {
		case tea.KeyEsc:
			m.betaniaTab = tabComments
			return m.openBETanIA(), nil
		case tea.KeyCtrlS:
			val := m.promptInput.Value()
			// If the admin saved the pre-filled default untouched, store empty so the
			// prompt stays the dynamic built-in rather than freezing a stale copy.
			if strings.TrimSpace(val) == strings.TrimSpace(m.svc.DefaultCommentPrompt()) {
				val = ""
			}
			if err := m.svc.SetCommentPromptOverride(m.user, val); err != nil {
				m.setStatus(err.Error(), true)
				return m, nil
			}
			m.betaniaTab = tabComments
			out := m.openBETanIA()
			if strings.TrimSpace(val) == "" {
				out.setStatus("comment prompt reset to the built-in default — press c to regenerate", false)
			} else {
				out.setStatus("comment prompt saved — press c to regenerate comments", false)
			}
			return out, nil
		}
	}
	var cmd tea.Cmd
	m.promptInput, cmd = m.promptInput.Update(msg)
	return m, cmd
}

// viewAIPrompt renders the override editor.
func (m Model) viewAIPrompt() string {
	out := titleStyle.Render("⚙  BETanIA: comment prompt") + "\n\n"
	out += helpStyle.Render("Replaces BETanIA's comment persona/tone/rules. The standings data and the") + "\n"
	out += helpStyle.Render("submit-comments instruction are always appended automatically.") + "\n"
	out += helpStyle.Render("Pre-filled with the current default — edit it, or clear it to keep the default.") + "\n\n"
	out += m.promptInput.View() + "\n\n"
	out += statusLine(m) + helpStyle.Render("ctrl+s: save · esc: cancel")
	return out
}

// ---- small shared helpers ---------------------------------------------------

// cursorRow renders a selectable list row with the shared cursor styling.
func cursorRow(selected bool, text string) string {
	if selected {
		return cursorOn.Render("▸ " + text)
	}
	return "  " + labelStyle.Render(text)
}

func nameByID(pts []service.PlayerTone, id int64) string {
	for _, p := range pts {
		if p.User.ID == id {
			return p.User.DisplayName
		}
	}
	return "?"
}

func newCtxInput(placeholder string) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.CharLimit = 200
	ti.Width = 60
	ti.Focus()
	return ti
}
