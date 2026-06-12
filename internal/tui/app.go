// Package tui implements BEThoven's terminal UI with Bubble Tea. It is a thin
// presentation layer: every action delegates to the service, which owns the
// rules. The same service methods are exercised directly by the integration
// tests.
package tui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"bethoven/internal/models"
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
	betMatch  models.Match
	betInputs []textinput.Model
	betFocus  int

	// my results
	myRows  []service.MatchResult
	myTotal int

	// leaderboard
	standings []service.Standing

	// per-match ranking
	rankMatch  *models.Match
	rankRows   []service.MatchStanding
	rankSearch searchBox

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
	gridPublic bool

	// settings screen (admin) + cached public_bets flag (drives the player's
	// "All players' bets" menu entry).
	settingsCursor int
	publicBets     bool
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
		return m.updateList(msg)
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
