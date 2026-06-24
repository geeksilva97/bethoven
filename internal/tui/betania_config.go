package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/service"
)

// ctxMode drives the rivalry/context editor's small wizard.
const (
	ctxModeList           = iota // browsing rivalries + notes
	ctxModeNote                  // typing a house note (general or player-bound)
	ctxModeNotePickPlayer        // picking who a new house note is about (or General)
	ctxModePickA                 // picking the first player of a rivalry
	ctxModePickB                 // picking the second player
	ctxModeRivalNote             // typing the rivalry note
	ctxModeDetail                // reading one entry's full content
	ctxModeEdit                  // editing an existing entry's text
)

// ctx entry kinds, for the detail/edit flow.
const (
	ctxKindRivalry    = iota
	ctxKindNote       // pool-wide house note (no player)
	ctxKindDerived    // BETanIA's auto per-game story notes
	ctxKindAuto       // BETanIA's self-managed rivalries
	ctxKindPlayerNote // house note bound to one player
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
	m.aiUsage, _ = m.svc.AIUsage(m.user)
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
	m.ctxAuto, _ = m.svc.AutoRivalriesView(m.user)
	m.ctxDerived, _ = m.svc.DerivedNotes(m.user)
	m.ctxMode, m.ctxCursor = ctxModeList, 0
	m.screen = screenAIContext
	return m
}

func (m *Model) reloadCtx() {
	if v, err := m.svc.CommentContextView(m.user); err == nil {
		m.ctxView = v
	}
	m.ctxAuto, _ = m.svc.AutoRivalriesView(m.user)
	m.ctxDerived, _ = m.svc.DerivedNotes(m.user)
}

// ctxTotal is the number of selectable rows: admin rivalries, auto rivalries, player
// notes, house notes, then derived notes.
func (m Model) ctxTotal() int {
	return len(m.ctxView.Rivalries) + len(m.ctxAuto) + len(m.ctxView.PlayerNotes) + len(m.ctxView.Notes) + len(m.ctxDerived)
}

// ctxRowAt maps a list cursor to its tier and the local index within that tier. Order
// matches viewCtxList: admin rivalries, auto rivalries, player notes, house notes, derived.
func (m Model) ctxRowAt(cursor int) (kind, idx int) {
	nRiv := len(m.ctxView.Rivalries)
	nAuto := len(m.ctxAuto)
	nPlayer := len(m.ctxView.PlayerNotes)
	nNote := len(m.ctxView.Notes)
	switch {
	case cursor < nRiv:
		return ctxKindRivalry, cursor
	case cursor < nRiv+nAuto:
		return ctxKindAuto, cursor - nRiv
	case cursor < nRiv+nAuto+nPlayer:
		return ctxKindPlayerNote, cursor - nRiv - nAuto
	case cursor < nRiv+nAuto+nPlayer+nNote:
		return ctxKindNote, cursor - nRiv - nAuto - nPlayer
	default:
		return ctxKindDerived, cursor - nRiv - nAuto - nPlayer - nNote
	}
}

// compactNotesMsg carries the result of an async derived-notes compaction (a model
// call, so it runs off the UI thread).
type compactNotesMsg struct{ err error }

// compactNotesCmd fuses the derived-notes diary into one narrative off the UI thread
// (the model call takes seconds), delivering the result as a compactNotesMsg.
func (m Model) compactNotesCmd() tea.Cmd {
	svc, user := m.svc, m.user
	return func() tea.Msg {
		return compactNotesMsg{err: svc.CompactDerivedNotes(user)}
	}
}

// compactHouseNotesMsg carries the result of an async house-notes compaction (a model
// call, so it runs off the UI thread).
type compactHouseNotesMsg struct{ err error }

// compactHouseNotesCmd fuses the admin's free-text house notes into one note off the
// UI thread, delivering the result as a compactHouseNotesMsg.
func (m Model) compactHouseNotesCmd() tea.Cmd {
	svc, user := m.svc, m.user
	return func() tea.Msg {
		return compactHouseNotesMsg{err: svc.CompactCommentNotes(user)}
	}
}

func (m Model) updateAIContext(msg tea.Msg) (tea.Model, tea.Cmd) {
	if cm, ok := msg.(compactNotesMsg); ok {
		if cm.err != nil {
			m.setStatus("compact failed: "+cm.err.Error(), true)
			return m, nil
		}
		m.setStatus("derived notes fused into one narrative", false)
		m.reloadCtx()
		m.clampCtxCursor()
		return m, nil
	}
	if cm, ok := msg.(compactHouseNotesMsg); ok {
		if cm.err != nil {
			m.setStatus("compact failed: "+cm.err.Error(), true)
			return m, nil
		}
		m.setStatus("house notes fused into one", false)
		m.reloadCtx()
		m.clampCtxCursor()
		return m, nil
	}
	switch m.ctxMode {
	case ctxModeNote, ctxModeRivalNote, ctxModeEdit:
		return m.updateCtxInput(msg)
	case ctxModeNotePickPlayer:
		return m.updateCtxNotePick(msg)
	case ctxModePickA, ctxModePickB:
		return m.updateCtxPick(msg)
	case ctxModeDetail:
		return m.updateCtxDetail(msg)
	default:
		return m.updateCtxList(msg)
	}
}

// openCtxDetail loads the selected row (rivalry, house note, or derived note) into
// the read-full view.
func (m Model) openCtxDetail() Model {
	kind, i := m.ctxRowAt(m.ctxCursor)
	m.ctxDetailKind, m.ctxDetailIdx = kind, i
	switch kind {
	case ctxKindRivalry:
		r := m.ctxView.Rivalries[i]
		m.ctxDetailTitle = fmt.Sprintf("%s vs %s", r.A, r.B)
		m.ctxDetailFull = r.Note
	case ctxKindAuto:
		if i < 0 || i >= len(m.ctxAuto) {
			return m
		}
		r := m.ctxAuto[i]
		m.ctxDetailTitle = fmt.Sprintf("%s vs %s (auto)", r.A, r.B)
		m.ctxDetailFull = r.Note
	case ctxKindPlayerNote:
		if i < 0 || i >= len(m.ctxView.PlayerNotes) {
			return m
		}
		n := m.ctxView.PlayerNotes[i]
		m.ctxDetailTitle = "Note about " + n.Player
		m.ctxDetailFull = n.Note
	case ctxKindNote:
		m.ctxDetailTitle = "House note"
		m.ctxDetailFull = m.ctxView.Notes[i]
	default:
		if i < 0 || i >= len(m.ctxDerived) {
			return m
		}
		m.ctxDetailTitle = "Derived note (auto)"
		m.ctxDetailFull = m.ctxDerived[i].Text
	}
	m.ctxMode = ctxModeDetail
	return m
}

// updateCtxDetail handles the read-full view: e edits (rivalries/house notes
// only), d deletes, esc returns to the list.
func (m Model) updateCtxDetail(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "esc", "b":
		m.ctxMode = ctxModeList
		return m, nil
	case "e":
		if m.ctxDetailKind == ctxKindDerived {
			m.setStatus("derived notes are auto-generated — delete or compact instead", true)
			return m, nil
		}
		m.ctxMode = ctxModeEdit
		m.ctxArea = newCtxArea("edit the text…", m.ctxDetailFull)
		return m, textarea.Blink
	case "p":
		// Pin/unpin a self-managed rivalry: pinned ⇒ BETanIA keeps it verbatim.
		if m.ctxDetailKind != ctxKindAuto || m.ctxDetailIdx >= len(m.ctxAuto) {
			return m, nil
		}
		pin := !m.ctxAuto[m.ctxDetailIdx].Pinned
		if err := m.svc.PinAutoRivalry(m.user, m.ctxDetailIdx, pin); err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.reloadCtx()
		m.setStatus(map[bool]string{true: "rivalry pinned", false: "rivalry unpinned"}[pin], false)
		return m, nil
	case "d":
		var err error
		switch m.ctxDetailKind {
		case ctxKindRivalry:
			err = m.svc.DeleteRivalry(m.user, m.ctxDetailIdx)
		case ctxKindAuto:
			err = m.svc.DeleteAutoRivalry(m.user, m.ctxDetailIdx)
		case ctxKindPlayerNote:
			err = m.svc.DeletePlayerNote(m.user, m.ctxDetailIdx)
		case ctxKindNote:
			err = m.svc.DeleteCommentNote(m.user, m.ctxDetailIdx)
		default:
			err = m.svc.DeleteDerivedNote(m.user, m.ctxDetailIdx)
		}
		if err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.reloadCtx()
		m.clampCtxCursor()
		m.ctxMode = ctxModeList
		return m, nil
	}
	return m, nil
}

func (m Model) updateCtxList(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	total := m.ctxTotal()
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
	case "enter":
		if total == 0 {
			return m, nil
		}
		return m.openCtxDetail(), nil
	case "n":
		// Choose who the note is about first (a player, or General/pool-wide), so a
		// player-bound note can be attributed and never leak onto someone else.
		m.ctxMode, m.ctxPickCursor = ctxModeNotePickPlayer, 0
		return m, nil
	case "a":
		if len(m.tonePlayers) < 2 {
			m.setStatus("need at least two players for a rivalry", true)
			return m, nil
		}
		m.ctxMode, m.ctxPickCursor = ctxModePickA, 0
		return m, nil
	case "c":
		// Fuse BETanIA's per-game derived "story" notes into ONE consolidated
		// narrative. This is a model call, so run it off the UI thread.
		m.setStatus("fusing derived notes…", false)
		return m, m.compactNotesCmd()
	case "f":
		// Fuse the admin's free-text house notes into ONE consolidated note (a model
		// call, run off the UI thread). Lossless merge — distinct facts are kept.
		if len(m.ctxView.Notes) < 2 {
			m.setStatus("need at least two house notes to fuse", true)
			return m, nil
		}
		m.setStatus("fusing house notes…", false)
		return m, m.compactHouseNotesCmd()
	case "C":
		// Clear all derived notes (the next finished match regenerates one).
		if err := m.svc.ClearDerivedNotes(m.user); err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.setStatus("derived notes cleared", false)
		m.reloadCtx()
		m.clampCtxCursor()
	case "R":
		// Clear BETanIA's self-managed rivalries (the next settle pass may repopulate).
		if err := m.svc.ClearAutoRivalries(m.user); err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.setStatus("auto-rivalries cleared", false)
		m.reloadCtx()
		m.clampCtxCursor()
	case "p":
		// Pin/unpin the selected auto-rivalry (no-op on other tiers).
		kind, i := m.ctxRowAt(m.ctxCursor)
		if kind != ctxKindAuto || i >= len(m.ctxAuto) {
			return m, nil
		}
		pin := !m.ctxAuto[i].Pinned
		if err := m.svc.PinAutoRivalry(m.user, i, pin); err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.reloadCtx()
		m.setStatus(map[bool]string{true: "rivalry pinned", false: "rivalry unpinned"}[pin], false)
	case "d":
		if total == 0 {
			return m, nil
		}
		kind, i := m.ctxRowAt(m.ctxCursor)
		var err error
		switch kind {
		case ctxKindRivalry:
			err = m.svc.DeleteRivalry(m.user, i)
		case ctxKindAuto:
			err = m.svc.DeleteAutoRivalry(m.user, i)
		case ctxKindPlayerNote:
			err = m.svc.DeletePlayerNote(m.user, i)
		case ctxKindNote:
			err = m.svc.DeleteCommentNote(m.user, i)
		default:
			err = m.svc.DeleteDerivedNote(m.user, i)
		}
		if err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.reloadCtx()
		m.clampCtxCursor()
	}
	return m, nil
}

// clampCtxCursor keeps the selection in range after the list shrinks.
func (m *Model) clampCtxCursor() {
	if n := m.ctxTotal(); m.ctxCursor >= n && m.ctxCursor > 0 {
		m.ctxCursor = n - 1
	}
}

func (m Model) updateCtxInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.Type {
		case tea.KeyEsc:
			m.ctxMode = ctxModeList
			return m, nil
		case tea.KeyCtrlS:
			text := strings.TrimSpace(m.ctxArea.Value())
			if text == "" {
				m.setStatus("type something first (esc to cancel)", true)
				return m, nil
			}
			var err error
			switch {
			case m.ctxMode == ctxModeEdit && m.ctxDetailKind == ctxKindRivalry:
				err = m.svc.EditRivalry(m.user, m.ctxDetailIdx, text)
			case m.ctxMode == ctxModeEdit && m.ctxDetailKind == ctxKindAuto:
				err = m.svc.EditAutoRivalry(m.user, m.ctxDetailIdx, text)
			case m.ctxMode == ctxModeEdit && m.ctxDetailKind == ctxKindPlayerNote:
				err = m.svc.EditPlayerNote(m.user, m.ctxDetailIdx, text)
			case m.ctxMode == ctxModeEdit:
				err = m.svc.EditCommentNote(m.user, m.ctxDetailIdx, text)
			case m.ctxMode == ctxModeRivalNote:
				err = m.svc.AddRivalry(m.user, m.ctxRivalA, m.ctxRivalB, text)
			case m.ctxNotePlayer != 0:
				// A new note bound to a specific player (chosen in the note picker).
				err = m.svc.AddPlayerNote(m.user, m.ctxNotePlayer, text)
			default:
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
	m.ctxArea, cmd = m.ctxArea.Update(msg)
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
		m.ctxArea = newCtxArea(fmt.Sprintf("why are %s and %s rivals?…", m.ctxRivalAName, sel.DisplayName), "")
		return m, textarea.Blink
	}
	return m, nil
}

// updateCtxNotePick handles choosing who a new house note is about: row 0 is the
// pool-wide "General" option, the rest are players. Selecting one opens the note
// textarea — General saves via AddCommentNote, a player via AddPlayerNote (the
// ctxNotePlayer != 0 sentinel, set here, drives that branch in updateCtxInput).
func (m Model) updateCtxNotePick(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		if m.ctxPickCursor < len(m.tonePlayers) { // row 0 = General, then one per player
			m.ctxPickCursor++
		}
	case "enter":
		if m.ctxPickCursor == 0 {
			m.ctxNotePlayer, m.ctxNotePlayerName = 0, ""
			m.ctxMode = ctxModeNote
			m.ctxArea = newCtxArea("a house note about the pool…", "")
			return m, textarea.Blink
		}
		sel := m.tonePlayers[m.ctxPickCursor-1].User
		m.ctxNotePlayer, m.ctxNotePlayerName = sel.ID, sel.DisplayName
		m.ctxMode = ctxModeNote
		m.ctxArea = newCtxArea(fmt.Sprintf("a note about %s…", sel.DisplayName), "")
		return m, textarea.Blink
	}
	return m, nil
}

func (m Model) viewAIContext() string {
	switch m.ctxMode {
	case ctxModeNote:
		title := "add house note"
		if m.ctxNotePlayer != 0 {
			title = "add note about " + m.ctxNotePlayerName
		}
		return titleStyle.Render("⚙  BETanIA: "+title) + "\n\n" +
			m.ctxArea.View() + "\n\n" + helpStyle.Render("ctrl+s: save · enter: new line · esc: cancel")
	case ctxModeNotePickPlayer:
		return m.viewCtxNotePick()
	case ctxModeRivalNote:
		head := fmt.Sprintf("%s  vs  %s", m.ctxRivalAName, nameByID(m.tonePlayers, m.ctxRivalB))
		return titleStyle.Render("⚙  BETanIA: rivalry note") + "\n\n" +
			labelStyle.Render(head) + "\n\n" + m.ctxArea.View() + "\n\n" +
			helpStyle.Render("ctrl+s: save · enter: new line · esc: cancel")
	case ctxModePickA, ctxModePickB:
		return m.viewCtxPick()
	case ctxModeDetail:
		return m.viewCtxDetail()
	case ctxModeEdit:
		return titleStyle.Render("⚙  BETanIA: edit "+m.ctxDetailTitle) + "\n\n" +
			m.ctxArea.View() + "\n\n" + helpStyle.Render("ctrl+s: save · enter: new line · esc: cancel")
	default:
		return m.viewCtxList()
	}
}

// viewCtxDetail shows one entry's full, wrapped content for reading.
func (m Model) viewCtxDetail() string {
	out := titleStyle.Render("⚙  BETanIA: "+m.ctxDetailTitle) + "\n\n"
	w := m.width - 6
	if w < 20 {
		w = 60
	}
	for _, line := range wrapText(m.ctxDetailFull, w) {
		out += line + "\n"
	}
	out += "\n" + statusLine(m)
	switch m.ctxDetailKind {
	case ctxKindDerived:
		out += helpStyle.Render("d: delete · esc: back · q: quit")
	case ctxKindAuto:
		pin := "p: pin"
		if m.ctxDetailIdx < len(m.ctxAuto) && m.ctxAuto[m.ctxDetailIdx].Pinned {
			pin = "p: unpin"
		}
		out += helpStyle.Render("e: edit (pins) · " + pin + " · d: delete · esc: back · q: quit")
	default:
		out += helpStyle.Render("e: edit · d: delete · esc: back · q: quit")
	}
	return out
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

	out += "\n" + labelStyle.Render("Auto-rivalries (BETanIA's own — updated as games finish)") + "\n"
	if len(m.ctxAuto) == 0 {
		out += helpStyle.Render("  none yet — BETanIA adds these from the standings") + "\n"
	}
	for _, r := range m.ctxAuto {
		pin := ""
		if r.Pinned {
			pin = "📌 "
		}
		out += cursorRow(idx == m.ctxCursor, truncate(fmt.Sprintf("%s%s vs %s — %s", pin, r.A, r.B, r.Note), w)) + "\n"
		idx++
	}

	out += "\n" + labelStyle.Render("Player notes (about one player — used only in their comment)") + "\n"
	if len(m.ctxView.PlayerNotes) == 0 {
		out += helpStyle.Render("  none yet — press n and pick a player") + "\n"
	}
	for _, n := range m.ctxView.PlayerNotes {
		out += cursorRow(idx == m.ctxCursor, truncate(fmt.Sprintf("%s — %s", n.Player, n.Note), w)) + "\n"
		idx++
	}

	out += "\n" + labelStyle.Render("House notes (general — about the pool)") + "\n"
	if len(m.ctxView.Notes) == 0 {
		out += helpStyle.Render("  none yet") + "\n"
	}
	for _, n := range m.ctxView.Notes {
		out += cursorRow(idx == m.ctxCursor, truncate(n, w)) + "\n"
		idx++
	}

	out += "\n" + labelStyle.Render("Derived notes (auto — BETanIA's story of finished matches)") + "\n"
	if len(m.ctxDerived) == 0 {
		out += helpStyle.Render("  none yet — generated when a match finishes") + "\n"
	}
	for _, n := range m.ctxDerived {
		label := n.Text
		if !n.At.IsZero() {
			label = relativeAgo(m.svc.Now(), n.At) + " · " + label
		}
		out += cursorRow(idx == m.ctxCursor, truncate(label, w)) + "\n"
		idx++
	}

	out += "\n" + statusLine(m) +
		helpStyle.Render("enter: read/edit · a: add rivalry · n: add note · p: pin auto · d: delete · f: fuse house · c: fuse derived · C: clear derived · R: clear auto · ↑/↓: move · esc: back")
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

func (m Model) viewCtxNotePick() string {
	out := titleStyle.Render("⚙  BETanIA: add note — who is it about?") + "\n\n"
	out += cursorRow(m.ctxPickCursor == 0, "— General (about the pool, not one player) —") + "\n"
	for i, pt := range m.tonePlayers {
		out += cursorRow(m.ctxPickCursor == i+1, pt.User.DisplayName) + "\n"
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

// openCommentRegen opens the optional steering box for regenerating a single
// player's comment ('r' on the comment detail screen). The admin can type extra
// direction or leave it empty for a plain regeneration.
func (m Model) openCommentRegen(player string) (Model, tea.Cmd) {
	m.regenPlayer = player
	m.regenArea = newCtxArea("optional: extra direction for this comment (leave empty for a plain regen)…", "")
	m.status = ""
	m.screen = screenAICommentRegen
	return m, textarea.Blink
}

// updateAICommentRegen handles the steering box: ctrl+s regenerates with whatever
// was typed (empty ⇒ a plain regen), esc cancels back to the comment detail.
func (m Model) updateAICommentRegen(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.Type {
		case tea.KeyEsc:
			m.screen = screenAICommentDetail
			return m, nil
		case tea.KeyCtrlS:
			extra := strings.TrimSpace(m.regenArea.Value())
			m.screen = screenAICommentDetail
			m.setStatus("regenerando o comentário de "+m.regenPlayer+"… (aguarde ~10s)", false)
			return m, m.regenCommentCmd(m.regenPlayer, extra)
		}
	}
	var cmd tea.Cmd
	m.regenArea, cmd = m.regenArea.Update(msg)
	return m, cmd
}

// viewAICommentRegen renders the optional steering box.
func (m Model) viewAICommentRegen() string {
	out := titleStyle.Render("⚙  BETanIA: regenerate "+m.regenPlayer+"'s comment") + "\n\n"
	out += helpStyle.Render("Optionally steer this one regeneration (e.g. \"lean savage\", \"mention their") + "\n"
	out += helpStyle.Render("Brazil pick\"). It applies to this comment only and is never saved.") + "\n"
	out += helpStyle.Render("Leave it empty to just regenerate as usual.") + "\n\n"
	out += m.regenArea.View() + "\n\n"
	out += statusLine(m) + helpStyle.Render("ctrl+s: regenerate · esc: cancel")
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

// newCtxArea builds a focused multi-line editor for a rivalry/house note, prefilled
// with value (empty for a new note). Multi-line so long notes are comfortable to
// edit; ctrl+s saves, enter inserts a newline (see updateCtxInput).
func newCtxArea(placeholder, value string) textarea.Model {
	ta := textarea.New()
	ta.Placeholder = placeholder
	ta.CharLimit = 1000
	ta.SetWidth(72)
	ta.SetHeight(6)
	if value != "" {
		ta.SetValue(value)
		ta.CursorEnd()
	}
	ta.Focus()
	return ta
}
