// Package tui implements BEThoven's terminal UI with Bubble Tea. It is a thin
// presentation layer: every action delegates to the service, which owns the
// rules. The same service methods are exercised directly by the integration
// tests.
package tui

import (
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
	screenAllBets
	screenPublicBets // player-facing, kickoff-filtered; reuses the all-bets view
	screenSettings
	screenScoringRules    // read-only: explains the active scoring mode
	screenAnalytics       // admin-only: usage stats panel
	screenBETanIA         // admin-only: AI player status + activity
	screenAITones         // admin-only: per-player comment tone
	screenAIContext       // admin-only: rivalry + house-note context
	screenAICommentDetail // admin-only: full text of a single BETanIA comment
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
	betMatch  models.Match
	betInputs []textinput.Model
	betFocus  int

	// my results
	myRows  []service.MatchResult
	myTotal int

	// leaderboard (+ in-play matches shown as a live header; auto-refreshed).
	// leaderEpoch is bumped on each entry so stale tick loops from a prior visit
	// self-terminate instead of stacking.
	standings   []service.Standing
	liveMatches []models.Match
	leaderEpoch int
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

	// admin: add match
	addInputs []textinput.Model
	addFocus  int
	addPhase  models.Phase

	// admin: enter result
	resCursor int
	resInputs []textinput.Model
	resFocus  int
	resMatch  *models.Match
	resSearch searchBox

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

	// active scoring mode, cached for the settings selector and the player-facing
	// "How scoring works" screen.
	scoringMode scoring.Mode

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
	aiBets     []service.AIBet
	betaniaTab int
	// admin: BETanIA comment worker. aiCommentsDisabled is set when the comment
	// worker isn't running; commentTone is the active tone (toggled with 't').
	aiCommentsDisabled bool
	aiCommentStatus    ai.CommentStatus
	aiCommentActivity  []ai.CommentAction
	commentTone        string
	// aiCommentCursor selects a row in the Comments tab list (preview); enter opens
	// screenAICommentDetail for the full text.
	aiCommentCursor int

	// admin: BETanIA per-player tone editor (reached with 'u' on the panel).
	tonePlayers []service.PlayerTone
	toneCursor  int

	// admin: BETanIA rivalry/house-note context editor (reached with 'x').
	// ctxMode drives a small wizard: list → add-note / pick-A → pick-B → rivalry-note.
	ctxView       service.CommentContextView
	ctxCursor     int // over the combined rivalries-then-notes list
	ctxMode       int
	ctxInput      textinput.Model
	ctxPickCursor int
	ctxRivalA     int64
	ctxRivalAName string
	ctxRivalB     int64
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
		return m.updateList(msg) // simple scroll/back screens share this
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
	case screenAllBets, screenPublicBets:
		return m.updateAllBets(msg)
	case screenSettings:
		return m.updateSettings(msg)
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
	case screenAllBets, screenPublicBets:
		return m.viewAllBets()
	case screenSettings:
		return m.viewSettings()
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
