// Package tui implements BEThoven's terminal UI with Bubble Tea. It is a thin
// presentation layer: every action delegates to the service, which owns the
// rules. The same service methods are exercised directly by the integration
// tests.
package tui

import (
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/ai"
	"bethoven/internal/analytics"
	"bethoven/internal/models"
	"bethoven/internal/scoring"
	"bethoven/internal/service"
)

type screen int

const (
	screenRegister screen = iota
	screenMenu
	screenFixtures
	screenBet
	screenMyResults
	screenLeaderboard
	screenMatchRank
	screenAdminMenu
	screenAddMatch
	screenEnterResult
	screenConfirmDelete // admin-only: confirm removing a match (+ its bets)
	screenAllBets
	screenPublicBets // player-facing, kickoff-filtered; reuses the all-bets view
	screenSettings
	screenKnockouts       // read-only: group standings, third-place race + bracket
	screenScoringRules    // read-only: explains the active scoring mode
	screenAnalytics       // admin-only: usage stats panel
	screenBETanIA         // admin-only: AI player status + activity
	screenAITones         // admin-only: per-player comment tone
	screenAIContext       // admin-only: rivalry + house-note context
	screenAICommentDetail // admin-only: full text of a single BETanIA comment
	screenAICommentRegen  // admin-only: optional steering prompt before regenerating one comment
	screenAIPrompt        // admin-only: edit BETanIA's comment-prompt override
)

// Model is the root Bubble Tea model. A new one is created per SSH session.
type Model struct {
	svc         *service.Service
	fingerprint string
	isAdminKey  bool
	user        *models.User // nil until registered

	screen        screen
	width, height int
	status        string // transient banner
	statusErr     bool

	// registration form
	regInputs []textinput.Model
	regFocus  int

	// main menu
	menuItems  []menuItem
	menuCursor int

	// fixtures list
	fixtures  []models.Match
	fixCursor int
	// myBets maps match id -> the user's current pick, so the bet list can mark
	// games already bet on. Refreshed when the fixtures screen is entered and
	// after each save.
	myBets map[int64]models.Bet

	// fixtures filtering (bet screen only): default shows the next-24h window;
	// fixShowAll toggles the full schedule; fixSearch is the live "/" filter.
	fixShowAll bool
	fixSearch  searchBox

	// bet form
	betFormA  []models.FormOutcome // recent-form strip, TeamA (loaded in openBet)
	betFormB  []models.FormOutcome // recent-form strip, TeamB
	betGamesA []service.TeamGame   // played-games list, TeamA (loaded in openBet)
	betGamesB []service.TeamGame   // played-games list, TeamB
	betMatch  models.Match
	betInputs []textinput.Model
	betFocus  int

	// my results
	myRows  []service.MatchResult
	myTotal int
	// myCursor scrolls the My bets list; it indexes into the placed-bet rows
	// (un-bet matches are skipped) and is initialised to the most recent match on
	// entry so the list opens on recent games, not the oldest.
	myCursor int

	// leaderboard (+ in-play matches shown as a live header; auto-refreshed).
	// leaderEpoch is bumped on each entry so stale tick loops from a prior visit
	// self-terminate instead of stacking.
	standings   []service.Standing
	liveMatches []models.Match
	leaderEpoch int
	// liveCommentary is BETanIA's single general top-of-board line about the in-play
	// slate (empty when nothing is live or the worker isn't running).
	liveCommentary string
	// 'p' reveals every player's pick for the in-play matches (live-only, open to
	// all). livePicks is populated only while the reveal is on.
	revealLivePicks bool
	livePicks       []service.LiveMatchPicks
	// rowComments holds BETanIA's leaderboard comments keyed by user id. Scoped by
	// the service to the viewer's OWN comment (players and admins alike); shown when
	// the cycle below is off.
	rowComments map[int64]string
	// comment cycle: on by default for everyone EXCEPT muted players (selfMuted),
	// the leaderboard auto-rotates which player's comment is shown — own first, then
	// random others — on an interval, in place, without leaving the screen.
	// cycleEpoch ties a tick loop to the current toggle session so a stale loop
	// self-stops. cycleAll excludes muted players (service-side).
	cycleComments  bool
	cycleAll       map[int64]string
	cycleCurrentID int64
	cycleEpoch     int
	selfMuted      bool // the viewer is muted ⇒ no own comment, no cycle
	// hideComments is the viewer's own persisted "don't show BETanIA comments on the
	// leaderboard" preference (toggled with 'h'); when set, no comment shows and the
	// cycle doesn't run.
	hideComments bool

	// per-match ranking
	rankMatch  *models.Match
	rankRows   []service.MatchStanding
	rankSearch searchBox // match-picker filter (pick mode)
	// display mode: select a player to drill into their points breakdown.
	rankCursor       int
	rankPlayerSearch searchBox              // player filter within a game's ranking
	rankBreakdown    *service.MatchStanding // set => showing one player's breakdown

	// admin: add match. editMatchID is 0 in add mode, or the id being edited when
	// the add-match form was opened to correct an existing match.
	addInputs   []textinput.Model
	addFocus    int
	addPhase    models.Phase
	editMatchID int64

	// admin: enter result
	resCursor int
	resInputs []textinput.Model
	resFocus  int
	resMatch  *models.Match
	resSearch searchBox

	// admin: delete-match confirmation (reached with 'd' on the match list).
	delMatch *models.Match
	delBets  int

	// all-bets grid + by-match drill-down. gridPublic marks the player-facing
	// public view (kickoff-filtered, no admin framing) vs the admin grid.
	grid       *service.AllBetsGrid
	allCursor  int
	allMatch   *models.Match // set => showing one match's picks
	allSearch  searchBox
	allShowAll bool // admin grid: false => next-3-days window, true => full schedule
	gridPublic bool

	// settings screen (admin) + cached public_bets flag (drives the player's
	// "All players' bets" menu entry).
	settingsCursor int
	publicBets     bool

	// knockouts screen: live group tables + third-place race + bracket ladder.
	// koView toggles between the qualification view and the bracket (tab/←/→).
	ko         service.KnockoutPicture
	koView     int
	koScroll   int // vertical scroll offset for the bracket tree
	koTraceIdx int // projected tree: team index 0..31 whose path is lit, -1 = none

	// active scoring mode, cached for the settings selector and the player-facing
	// "How scoring works" screen.
	scoringMode scoring.Mode

	// active round-weight scheme, cached alongside scoringMode for the settings
	// selector and the "How scoring works" ladder.
	roundWeights scoring.WeightScheme

	// admin: analytics panel. anDisabled is set when the subsystem is off, so the
	// screen can explain that instead of showing empty stats.
	anDisabled bool
	anOverview analytics.Overview
	anTimeline []analytics.Bucket
	anPlayers  []analytics.PlayerStat
	anRecent   []analytics.Event

	// admin: BETanIA panel. aiDisabled is set when no worker is attached, so the
	// screen explains that instead of showing empty stats.
	aiDisabled bool
	aiStatus   ai.Status
	aiActivity []ai.Action
	// aiBets is BETanIA's picks on record, sourced from the DB (survives a restart,
	// unlike aiActivity's in-memory ring). betaniaTab selects which panel tab shows.
	// aiBetsCursor is the scroll cursor for the Betting tab picks feed.
	aiBets       []service.AIBet
	aiBetsCursor int
	betaniaTab   int
	// aiUsage is BETanIA's cumulative token usage + estimated cost, read from the
	// durable on-disk usage log (survives restarts). Shown on the Usage tab.
	// aiUsageOffset is the line scroll offset for the Usage tab.
	aiUsage       ai.UsageReport
	aiUsageOffset int
	// admin: BETanIA comment worker. aiCommentsDisabled is set when the comment
	// worker isn't running; commentTone is the active tone (toggled with 't').
	aiCommentsDisabled bool
	aiCommentStatus    ai.CommentStatus
	aiCommentActivity  []ai.CommentAction
	commentTone        string
	commentMood        string // BETanIA's current self-evolving mood (director-set)
	// aiCommentCursor selects a row in the Comments tab list (preview); enter opens
	// screenAICommentDetail for the full text.
	aiCommentCursor int

	// admin: BETanIA per-player tone editor (reached with 'u' on the panel).
	tonePlayers []service.PlayerTone
	toneCursor  int

	// admin: BETanIA rivalry/house-note context editor (reached with 'x').
	// ctxMode drives a small wizard: list → add-note / pick-A → pick-B → rivalry-note.
	ctxView       service.CommentContextView
	ctxAuto       []service.AutoRivalryView // BETanIA's self-managed rivalries
	ctxDerived    []service.DerivedNoteView // BETanIA's auto-derived "story" notes
	ctxCursor     int                       // over admin rivalries, auto rivalries, notes, then derived notes
	ctxMode       int
	ctxArea       textarea.Model // multi-line editor for rivalry/house notes (long text)
	ctxPickCursor int
	ctxRivalA     int64
	ctxRivalAName string
	ctxRivalB     int64
	// add-note target: the player a new house note is about (0 ⇒ General/pool-wide).
	// Set in the note picker, drives AddPlayerNote vs AddCommentNote at save.
	ctxNotePlayer     int64
	ctxNotePlayerName string
	// detail/edit of an existing context entry (enter on a row → read full; e → edit).
	ctxDetailKind  int    // ctxKindRivalry | ctxKindNote | ctxKindDerived
	ctxDetailIdx   int    // index within that kind's list
	ctxDetailTitle string // header line (e.g. "Alice vs Bob")
	ctxDetailFull  string // full untruncated content for reading

	// admin: BETanIA comment-prompt override editor (reached with 's' on the
	// Comments tab). promptInput edits the full instruction body (multi-line, since
	// the override is a whole prompt); empty ⇒ default.
	promptInput textarea.Model

	// admin: optional one-off steering before regenerating a single player's
	// comment (reached with 'r' on the comment detail screen). regenArea holds the
	// (optional) extra prompt; regenPlayer is the targeted player's display name.
	regenArea   textarea.Model
	regenPlayer string
}

// New builds the session model. user may be nil (unknown key → registration).
func New(svc *service.Service, fingerprint string, isAdminKey bool, user *models.User) Model {
	m := Model{
		svc:         svc,
		fingerprint: fingerprint,
		isAdminKey:  isAdminKey,
		user:        user,
	}
	if user == nil {
		m.screen = screenRegister
		m.initRegister()
	} else {
		m.screen = screenMenu
		m.publicBets, _ = svc.PublicBetsEnabled()
		m.buildMenu()
	}
	return m
}

func (m Model) Init() tea.Cmd { return textinput.Blink }

// Update routes input to the active screen's handler.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		// Global quit.
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
	case leaderTickMsg:
		// Live leaderboard refresh; self-stops on leave or on a superseded epoch.
		return m.onLeaderTick(msg)
	case cycleTickMsg:
		// Comment-cycle advance; self-stops on leave, toggle-off, or superseded epoch.
		return m.onCycleTick(msg)
	}

	switch m.screen {
	case screenRegister:
		return m.updateRegister(msg)
	case screenMenu:
		return m.updateMenu(msg)
	case screenFixtures:
		return m.updateFixtures(msg)
	case screenBet:
		return m.updateBet(msg)
	case screenMyResults:
		return m.updateMyResults(msg)
	case screenLeaderboard:
		return m.updateLeaderboard(msg)
	case screenMatchRank:
		return m.updateMatchRank(msg)
	case screenAdminMenu:
		return m.updateMenu(msg)
	case screenAddMatch:
		return m.updateAddMatch(msg)
	case screenEnterResult:
		return m.updateEnterResult(msg)
	case screenConfirmDelete:
		return m.updateConfirmDelete(msg)
	case screenAllBets, screenPublicBets:
		return m.updateAllBets(msg)
	case screenSettings:
		return m.updateSettings(msg)
	case screenKnockouts:
		return m.updateKnockouts(msg)
	case screenScoringRules:
		return m.updateScoringRules(msg)
	case screenAnalytics:
		return m.updateList(msg) // read-only: any key returns to the menu
	case screenBETanIA:
		return m.updateBETanIA(msg)
	case screenAITones:
		return m.updateAITones(msg)
	case screenAIContext:
		return m.updateAIContext(msg)
	case screenAICommentDetail:
		return m.updateAICommentDetail(msg)
	case screenAICommentRegen:
		return m.updateAICommentRegen(msg)
	case screenAIPrompt:
		return m.updateAIPrompt(msg)
	}
	return m, nil
}

// View renders the active screen.
func (m Model) View() string {
	switch m.screen {
	case screenRegister:
		return m.viewRegister()
	case screenMenu, screenAdminMenu:
		return m.viewMenu()
	case screenFixtures:
		return m.viewFixtures()
	case screenBet:
		return m.viewBet()
	case screenMyResults:
		return m.viewMyResults()
	case screenLeaderboard:
		return m.viewLeaderboard()
	case screenMatchRank:
		return m.viewMatchRank()
	case screenAddMatch:
		return m.viewAddMatch()
	case screenEnterResult:
		return m.viewEnterResult()
	case screenConfirmDelete:
		return m.viewConfirmDelete()
	case screenAllBets, screenPublicBets:
		return m.viewAllBets()
	case screenSettings:
		return m.viewSettings()
	case screenKnockouts:
		return m.viewKnockouts()
	case screenScoringRules:
		return m.viewScoringRules()
	case screenAnalytics:
		return m.viewAnalytics()
	case screenBETanIA:
		return m.viewBETanIA()
	case screenAITones:
		return m.viewAITones()
	case screenAIContext:
		return m.viewAIContext()
	case screenAICommentDetail:
		return m.viewAICommentDetail()
	case screenAICommentRegen:
		return m.viewAICommentRegen()
	case screenAIPrompt:
		return m.viewAIPrompt()
	}
	return ""
}

// setStatus shows a transient banner (cleared on the next navigation).
func (m *Model) setStatus(msg string, isErr bool) {
	m.status = msg
	m.statusErr = isErr
}

// updateList handles the read-only screens: any key returns to the menu.
func (m Model) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "q":
			return m, tea.Quit
		default:
			return m.goMenu(), nil
		}
	}
	return m, nil
}

// goMenu returns to the appropriate menu, clearing transient state.
func (m Model) goMenu() Model {
	m.status = ""
	m.screen = screenMenu
	m.publicBets, _ = m.svc.PublicBetsEnabled()
	m.buildMenu()
	return m
}
