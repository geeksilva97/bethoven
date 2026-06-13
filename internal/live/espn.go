package live

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
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
	return out, nil
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
		out = append(out, Event{
			Home:      home.name,
			Away:      away.name,
			HomeScore: home.score,
			AwayScore: away.score,
			Date:      parseESPNDate(ev.Date),
			State:     ParseState(comp.Status.Type.State),
			Minute:    comp.Status.Period,
			Clock:     comp.Status.DisplayClock,
		})
	}
	return out, nil
}
