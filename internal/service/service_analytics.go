package service

import (
	"errors"
	"time"

	"bethoven/internal/analytics"
	"bethoven/internal/models"
)

// AnalyticsSink is the optional usage-tracking port. The concrete implementation
// (analytics.Recorder) records events asynchronously to a SEPARATE database, so
// nothing here can block or fail the betting/scoring path. nil is valid and
// means "analytics disabled" — every emit site becomes a no-op. Mirrors the
// LiveStore port.
type AnalyticsSink interface {
	Track(ev analytics.Event)
	Overview(now time.Time) (analytics.Overview, error)
	Recent(limit int) ([]analytics.Event, error)
	PerPlayer() ([]analytics.PlayerStat, error)
	Timeline(now time.Time, days int) ([]analytics.Bucket, error)
}

// ErrAnalyticsOff is returned by the admin query methods when analytics is not
// enabled, so the TUI can show an explanatory message instead of an error.
var ErrAnalyticsOff = errors.New("analytics is disabled")

// Event names. These are the authoritative list; the analytics query layer
// mirrors the two it aggregates on (session_start, bet_placed).
const (
	EvSession        = "session_start"   // an SSH session was opened (an "access")
	EvView           = "view"            // a player navigated to a screen (props: screen)
	EvRegistered     = "registered"      // a new account was created
	EvBetPlaced      = "bet_placed"      // a pick was created or updated
	EvResultEntered  = "result_entered"  // admin recorded a match result
	EvMatchAdded     = "match_added"     // admin added a fixture
	EvMatchEdited    = "match_edited"    // admin edited a fixture's teams/phase/group/kickoff
	EvMatchDeleted   = "match_deleted"   // admin removed a fixture
	EvSettingChanged = "setting_changed" // admin changed a setting
	EvCardGenerated  = "card_generated"  // admin generated BETanIA player card(s)
)

// Track records an event for callers that already hold the acting user (the
// server's session_start, the TUI's screen views). Safe to call when analytics
// is off. u may be nil (unregistered key on first connect).
func (s *Service) Track(u *models.User, fingerprint, name string, props map[string]string) {
	s.track(u, fingerprint, name, props)
}

// track is the internal emit helper. It never blocks and never errors.
func (s *Service) track(u *models.User, fingerprint, name string, props map[string]string) {
	if s.analytics == nil {
		return
	}
	ev := analytics.Event{
		At:          s.Now(),
		Fingerprint: fingerprint,
		Actor:       "(unregistered)",
		Name:        name,
		Props:       props,
	}
	if u != nil {
		ev.UserID = u.ID
		ev.Actor = u.DisplayName
	}
	s.analytics.Track(ev)
}

// trackByID emits an event when only the user id is known (e.g. PlaceBet). It
// does NO database work — it records just the id, leaving the actor's display
// name to be resolved at read time (see AnalyticsRecent / AnalyticsPerPlayer).
// This keeps the betting hot path free of any extra read against the
// single-connection domain DB.
func (s *Service) trackByID(userID int64, name string, props map[string]string) {
	if s.analytics == nil {
		return
	}
	s.analytics.Track(analytics.Event{
		At:     s.Now(),
		UserID: userID,
		Name:   name,
		Props:  props,
	})
}

// --- Admin read side ------------------------------------------------------

// AnalyticsOverview returns the headline KPIs. Admin only. RegisteredPlayers is
// taken from the domain user table so it counts players who registered before
// analytics was turned on.
func (s *Service) AnalyticsOverview(by *models.User) (analytics.Overview, error) {
	if err := requireAdmin(by); err != nil {
		return analytics.Overview{}, err
	}
	if s.analytics == nil {
		return analytics.Overview{}, ErrAnalyticsOff
	}
	ov, err := s.analytics.Overview(s.Now())
	if err != nil {
		return analytics.Overview{}, err
	}
	if users, err := s.store.AllUsers(); err == nil {
		ov.RegisteredPlayers = len(users)
	}
	return ov, nil
}

// AnalyticsRecent returns the most recent events, newest first. Admin only.
// Events recorded with only a user id (e.g. bets) get their actor filled in here
// from the domain user table — name resolution happens at read time, never on
// the emit path.
func (s *Service) AnalyticsRecent(by *models.User, limit int) ([]analytics.Event, error) {
	if err := requireAdmin(by); err != nil {
		return nil, err
	}
	if s.analytics == nil {
		return nil, ErrAnalyticsOff
	}
	events, err := s.analytics.Recent(limit)
	if err != nil {
		return nil, err
	}
	names := s.userNames()
	for i := range events {
		if n, ok := names[events[i].UserID]; ok {
			events[i].Actor = n
		}
	}
	return events, nil
}

// userNames maps user id → current display name from the domain store, for
// labeling analytics rows at read time. Best-effort: returns an empty map on
// error so the panel still renders.
func (s *Service) userNames() map[int64]string {
	users, err := s.store.AllUsers()
	if err != nil {
		return map[int64]string{}
	}
	m := make(map[int64]string, len(users))
	for _, u := range users {
		m[u.ID] = u.DisplayName
	}
	return m
}

// AnalyticsPerPlayer returns per-player engagement, busiest first. Admin only.
// The actor label is refreshed to each player's current display name where the
// user id is known (the stored name is a snapshot from event time).
func (s *Service) AnalyticsPerPlayer(by *models.User) ([]analytics.PlayerStat, error) {
	if err := requireAdmin(by); err != nil {
		return nil, err
	}
	if s.analytics == nil {
		return nil, ErrAnalyticsOff
	}
	rows, err := s.analytics.PerPlayer()
	if err != nil {
		return nil, err
	}
	names := s.userNames()
	for i := range rows {
		if n, ok := names[rows[i].UserID]; ok {
			rows[i].Actor = n
		}
	}
	return rows, nil
}

// AnalyticsTimeline returns access counts per day for the last `days` days.
// Admin only.
func (s *Service) AnalyticsTimeline(by *models.User, days int) ([]analytics.Bucket, error) {
	if err := requireAdmin(by); err != nil {
		return nil, err
	}
	if s.analytics == nil {
		return nil, ErrAnalyticsOff
	}
	return s.analytics.Timeline(s.Now(), days)
}
