package live

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"bethoven/internal/models"
)

// espnBase is ESPN's unofficial, keyless scoreboard API. No token or signup is
// required; it returns JSON. It is undocumented and may change without notice —
// which is exactly why it lives behind the Provider interface.
const espnBase = "https://site.api.espn.com/apis/site/v2/sports/soccer"

// DefaultLeague is ESPN's slug for the FIFA World Cup.
const DefaultLeague = "fifa.world"

// espnDateLayouts are the kickoff formats ESPN emits. It uses minute precision
// without seconds ("2026-06-12T19:00Z"), which time.RFC3339 rejects, so we try
// that first and fall back to RFC3339 for safety.
var espnDateLayouts = []string{"2006-01-02T15:04Z07:00", time.RFC3339}

// parseESPNDate parses a kickoff timestamp, returning a zero time if none of the
// known layouts match (the resolver tolerates a zero date by matching on teams).
func parseESPNDate(s string) time.Time {
	for _, layout := range espnDateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// validScore rejects scores no real football match produces, so a malformed or
// hostile feed can't push garbage (or negatives) into the live cache.
func validScore(n int) bool { return n >= 0 && n <= 99 }

// cleanClock strips the feed's display clock to a safe, printable subset. The
// feed is UNTRUSTED and this string is rendered into every connected player's
// terminal, so — exactly like display names (see service.cleanName) — anything
// outside a small whitelist (digits and clock punctuation) is dropped to
// neutralize ANSI/control-char injection. Length-capped so it can't overrun the
// fixed-width table columns either.
func cleanClock(s string) string {
	const maxLen = 12
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= maxLen {
			break
		}
		if unicode.IsDigit(r) || strings.ContainsRune("'+:. ", r) {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// cleanOdds builds a sanitized, display-ready odds string from a provider's
// details ("USA -160") and over/under line. Like cleanClock, the feed is
// UNTRUSTED and this string renders into terminals (and later an AI comment), so
// the details are whitelisted to letters/digits/space plus a small punctuation
// set (team abbreviations like "USA" mean letters are allowed here, unlike the
// clock) and length-capped — strip, don't reject, to neutralize ANSI/control
// injection. Format is "<details> · O/U <overUnder>"; the O/U tail is omitted
// when the line is 0/absent, and the whole thing is empty when there are no
// details.
func cleanOdds(details string, overUnder float64) string {
	const maxLen = 32
	var b strings.Builder
	for _, r := range details {
		if b.Len() >= maxLen {
			break
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-+.·/' :", r) {
			b.WriteRune(r)
		}
	}
	clean := strings.TrimSpace(b.String())
	if clean == "" {
		return ""
	}
	if overUnder > 0 {
		return fmt.Sprintf("%s · O/U %s", clean, strconv.FormatFloat(overUnder, 'f', -1, 64))
	}
	return clean
}

// Key-event limits. The summary endpoint returns the whole match's key events;
// we keep only the most recent few (most-relevant for a live line) and cap the
// text lengths so untrusted feed prose can't overrun the prompt or a log line.
const (
	maxKeyEvents    = 12
	maxEventTextLen = 200
	maxEventTypeLen = 40
)

// cleanEventText strips ANSI escapes and control characters from an UNTRUSTED
// feed string (a key-event description — free-form prose that carries player
// names) and collapses whitespace to single spaces, then length-caps it. Unlike
// cleanClock/cleanOdds (small whitelists) this is prose, so it mirrors
// ai.sanitizeText: strip the dangerous bytes, keep the printable rest. The text
// renders into every player's terminal and into BETanIA's prompt, so it's the
// same ANSI-injection boundary as display names and model output.
func cleanEventText(s string, maxLen int) string {
	var b strings.Builder
	prevSpace := false
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		// Consume a whole CSI/escape sequence, not just the introducer (same as
		// ai.sanitizeText), so "\x1b[31m" leaves no inert "[31m" residue.
		if r == 0x1b || r == 0x9b {
			if r == 0x1b && i+1 < len(rs) && rs[i+1] == '[' {
				i++
			}
			for i+1 < len(rs) {
				n := rs[i+1]
				i++
				if n >= 0x40 && n <= 0x7e {
					break
				}
			}
			continue
		}
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v':
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
		case r < 0x20 || (r >= 0x7f && r <= 0x9f) || !unicode.IsPrint(r):
			// drop remaining C0/C1 controls and non-printable runes
		default:
			b.WriteRune(r)
			prevSpace = false
		}
	}
	return truncateRunes(strings.TrimSpace(b.String()), maxLen)
}

// truncateRunes caps s to at most maxLen runes (not bytes), trimming trailing space.
func truncateRunes(s string, maxLen int) string {
	rs := []rune(s)
	if len(rs) <= maxLen {
		return s
	}
	return strings.TrimSpace(string(rs[:maxLen]))
}

// ESPNProvider fetches live scores from ESPN's keyless scoreboard endpoint.
type ESPNProvider struct {
	league string
	client *http.Client
}

// NewESPNProvider builds a provider for the given league slug (e.g. "fifa.world").
func NewESPNProvider(league string) *ESPNProvider {
	if league == "" {
		league = DefaultLeague
	}
	return &ESPNProvider{
		league: league,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// espnResp is the slim shape we parse out of ESPN's scoreboard JSON.
type espnResp struct {
	Events []struct {
		ID           string `json:"id"`
		Date         string `json:"date"`
		Competitions []struct {
			Competitors []struct {
				HomeAway string `json:"homeAway"`
				Score    string `json:"score"`
				Team     struct {
					DisplayName string `json:"displayName"`
				} `json:"team"`
			} `json:"competitors"`
			Status struct {
				DisplayClock string `json:"displayClock"`
				Period       int    `json:"period"`
				Type         struct {
					State string `json:"state"`
				} `json:"type"`
			} `json:"status"`
			Odds []struct {
				Details   string  `json:"details"`
				OverUnder float64 `json:"overUnder"`
				Provider  struct {
					Priority int    `json:"priority"`
					Name     string `json:"name"`
				} `json:"provider"`
			} `json:"odds"`
		} `json:"competitions"`
	} `json:"events"`
}

// Fetch retrieves the scoreboard for each given UTC day and returns the parsed
// events. Per-day failures are tolerated: a day that errors is skipped so a
// single bad response doesn't blank the whole snapshot.
func (p *ESPNProvider) Fetch(ctx context.Context, days []time.Time) ([]Event, error) {
	var out []Event
	seen := make(map[string]bool) // dedupe across overlapping day windows
	for _, d := range days {
		evs, err := p.fetchDay(ctx, d)
		if err != nil {
			// Tolerate a single day's failure; the caller logs at a higher level.
			continue
		}
		for _, e := range evs {
			key := e.Home + "|" + e.Away + "|" + e.Date.Format(time.RFC3339)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, e)
		}
	}
	// Enrich IN-PLAY events with their key events (goals/cards) from the per-event
	// summary endpoint. Only in-play matches — pre/post don't need a live line, and
	// this is one extra call each, so we keep it to the handful that are live now.
	// Any failure is tolerated: the match just carries no events this pass.
	for i := range out {
		if out[i].State == StateIn && out[i].ID != "" {
			if kes, err := p.fetchKeyEvents(ctx, out[i].ID); err == nil {
				out[i].KeyEvents = kes
			}
		}
	}
	return out, nil
}

// fetchKeyEvents pulls one match's key events (goals, cards) from ESPN's summary
// endpoint. Returns nil (not an error to the caller) only on a successful decode;
// transport/status failures propagate so Fetch can tolerate them per-event.
func (p *ESPNProvider) fetchKeyEvents(ctx context.Context, eventID string) ([]models.MatchEvent, error) {
	url := fmt.Sprintf("%s/%s/summary?event=%s", espnBase, p.league, eventID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("espn summary: status %d", resp.StatusCode)
	}
	kes, err := decodeKeyEvents(resp.Body)
	_, _ = io.Copy(io.Discard, resp.Body) // drain for keep-alive
	return kes, err
}

// summaryResp is the slim shape we parse from ESPN's match-summary JSON. We read
// only keyEvents (the curated goals/cards/subs list); the full play-by-play
// commentary, lineups, and box score in the same payload are left for later.
type summaryResp struct {
	KeyEvents []struct {
		Type struct {
			Text string `json:"text"`
		} `json:"type"`
		Text  string `json:"text"`
		Clock struct {
			DisplayValue string `json:"displayValue"`
		} `json:"clock"`
		ScoringPlay bool `json:"scoringPlay"`
	} `json:"keyEvents"`
}

// decodeKeyEvents parses a summary body into sanitized key events, keeping the
// most recent maxKeyEvents (the tail of the chronological list). Split out from
// the HTTP path so it's testable against canned payloads. The feed is UNTRUSTED:
// every string is run through cleanEventText (strip ANSI/control, length-cap).
func decodeKeyEvents(r io.Reader) ([]models.MatchEvent, error) {
	var body summaryResp
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return nil, err
	}
	all := make([]models.MatchEvent, 0, len(body.KeyEvents))
	for _, ke := range body.KeyEvents {
		text := cleanEventText(ke.Text, maxEventTextLen)
		if text == "" {
			continue // nothing renderable (e.g. an empty delay marker)
		}
		all = append(all, models.MatchEvent{
			Clock:   cleanClock(ke.Clock.DisplayValue),
			Type:    cleanEventText(ke.Type.Text, maxEventTypeLen),
			Text:    text,
			Scoring: ke.ScoringPlay,
		})
	}
	if len(all) > maxKeyEvents {
		all = all[len(all)-maxKeyEvents:] // keep the most recent
	}
	return all, nil
}

func (p *ESPNProvider) fetchDay(ctx context.Context, day time.Time) ([]Event, error) {
	url := fmt.Sprintf("%s/%s/scoreboard?dates=%s", espnBase, p.league, day.UTC().Format("20060102"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("espn: status %d", resp.StatusCode)
	}

	evs, err := decodeEvents(resp.Body)
	// Drain any trailing bytes so the connection can be reused (keep-alive).
	_, _ = io.Copy(io.Discard, resp.Body)
	return evs, err
}

// decodeEvents parses an ESPN scoreboard JSON body into events. Split out from
// the HTTP path so it can be tested against canned payloads without a network.
func decodeEvents(r io.Reader) ([]Event, error) {
	var body espnResp
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return nil, err
	}

	out := make([]Event, 0, len(body.Events))
	for _, ev := range body.Events {
		if len(ev.Competitions) == 0 {
			continue
		}
		comp := ev.Competitions[0]
		var home, away struct {
			name  string
			score int
		}
		var haveHome, haveAway bool
		for _, c := range comp.Competitors {
			score, _ := strconv.Atoi(c.Score) // "" -> 0, which is fine pre-match
			switch c.HomeAway {
			case "home":
				home.name, home.score, haveHome = c.Team.DisplayName, score, true
			case "away":
				away.name, away.score, haveAway = c.Team.DisplayName, score, true
			}
		}
		if !haveHome || !haveAway {
			continue // malformed event; skip rather than guess
		}
		// The feed is untrusted: reject impossible scores so garbage never reaches
		// the cache, provisional scoring, or the screen.
		if !validScore(home.score) || !validScore(away.score) {
			continue
		}
		// Pick the primary (priority 1) provider's odds, falling back to the first
		// entry when none is flagged primary. Absent odds degrade to an empty string.
		var odds string
		if len(comp.Odds) > 0 {
			pick := comp.Odds[0]
			for _, o := range comp.Odds {
				if o.Provider.Priority == 1 {
					pick = o
					break
				}
			}
			odds = cleanOdds(pick.Details, pick.OverUnder)
		}
		out = append(out, Event{
			ID:        ev.ID,
			Home:      home.name,
			Away:      away.name,
			HomeScore: home.score,
			AwayScore: away.score,
			Date:      parseESPNDate(ev.Date),
			State:     ParseState(comp.Status.Type.State),
			Minute:    comp.Status.Period,
			Clock:     cleanClock(comp.Status.DisplayClock),
			Odds:      odds,
		})
	}
	return out, nil
}
