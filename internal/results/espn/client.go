// Package espn implements results.Fetcher against ESPN's public soccer
// scoreboard endpoint. It is the only place in the results path that does HTTP;
// it maps ESPN's JSON into the I/O-free results.FeedMatch type.
//
// The endpoint needs no API key — that's the whole reason we use it. It is
// unofficial/undocumented, so treat its shape as something to re-verify (run
// `bethoven check-feed`) rather than a contract.
package espn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"bethoven/internal/results"
)

const defaultBaseURL = "https://site.api.espn.com/apis/site/v2/sports/soccer"

// wcDateRange bounds the scoreboard query to the 2026 World Cup window
// (2026-06-11 .. 2026-07-19, the final). We pull the WHOLE window every poll in
// one request, so the poller is self-healing: a match that finished while the
// server was down is still picked up on the next poll rather than lost. The
// scoreboard caps at 100 events unless limit is raised, so we pass limit=500
// (WC has 104 matches).
const (
	wcDateRange = "20260611-20260719"
	eventLimit  = "500"
)

// espnDate is ESPN's timestamp layout: RFC3339 WITHOUT seconds, e.g.
// "2026-06-11T19:00Z". time.RFC3339 requires seconds and would fail to parse it.
const espnDate = "2006-01-02T15:04Z07:00"

// Client fetches a league's scoreboard from ESPN.
type Client struct {
	league  string // ESPN league slug, e.g. "fifa.world"
	baseURL string
	http    *http.Client
}

// New builds a client. A blank baseURL uses the production endpoint; tests pass
// a local test-server URL.
func New(league, baseURL string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		league:  league,
		baseURL: baseURL,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// Fetch retrieves the league's scoreboard and maps it to feed matches.
func (c *Client) Fetch(ctx context.Context) ([]results.FeedMatch, error) {
	endpoint := fmt.Sprintf("%s/%s/scoreboard?dates=%s&limit=%s",
		c.baseURL, url.PathEscape(c.league), wcDateRange, eventLimit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("espn: GET %s: status %d", endpoint, resp.StatusCode)
	}

	var payload scoreboardResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("espn: decode: %w", err)
	}
	return payload.toFeed()
}

// --- JSON shapes (subset of the ESPN scoreboard response) ---

type scoreboardResponse struct {
	Events []event `json:"events"`
}

type event struct {
	ID           string        `json:"id"`
	Date         string        `json:"date"`
	Competitions []competition `json:"competitions"`
}

type competition struct {
	Status      status       `json:"status"`
	Competitors []competitor `json:"competitors"`
}

type status struct {
	Type statusType `json:"type"`
}

type statusType struct {
	// Name is ESPN's status code, e.g. STATUS_SCHEDULED, STATUS_SECOND_HALF,
	// STATUS_FULL_TIME. Completed is true once the match is over (in any way).
	Name      string `json:"name"`
	Completed bool   `json:"completed"`
}

type competitor struct {
	HomeAway string `json:"homeAway"` // "home" or "away"
	Score    string `json:"score"`    // ESPN reports the score as a string, e.g. "2"
	Team     team   `json:"team"`
}

type team struct {
	DisplayName string `json:"displayName"`
}

func (r scoreboardResponse) toFeed() ([]results.FeedMatch, error) {
	out := make([]results.FeedMatch, 0, len(r.Events))
	for _, ev := range r.Events {
		if len(ev.Competitions) == 0 {
			continue
		}
		comp := ev.Competitions[0]

		kickoff, err := time.Parse(espnDate, ev.Date)
		if err != nil {
			return nil, fmt.Errorf("event %s: bad date %q: %w", ev.ID, ev.Date, err)
		}

		home, away, ok := homeAway(comp.Competitors)
		if !ok {
			continue // malformed entry (not exactly one home + one away)
		}

		fm := results.FeedMatch{
			ExternalRef: ev.ID,
			HomeTeam:    home.Team.DisplayName,
			AwayTeam:    away.Team.DisplayName,
			KickoffUTC:  kickoff.UTC(),
			Finished:    comp.Status.Type.Completed,
		}
		if fm.Finished {
			fm.Reg90 = reg90(comp.Status.Type.Name, home.Score, away.Score)
		}
		out = append(out, fm)
	}
	return out, nil
}

// homeAway picks the home and away competitors out of the pair.
func homeAway(cs []competitor) (home, away competitor, ok bool) {
	var gotH, gotA bool
	for _, c := range cs {
		switch c.HomeAway {
		case "home":
			home, gotH = c, true
		case "away":
			away, gotA = c, true
		}
	}
	return home, away, gotH && gotA
}

// reg90 extracts the regulation 90-minute score, or nil if it can't be derived
// safely.
//
// IMPORTANT: the scoreboard exposes ONLY the final score (no per-period
// breakdown), so for a knockout that went to extra time the score string is the
// post-ET result, NOT the 90' score BEThoven needs. We therefore trust the
// score only when the match finished in regulation — ESPN reports that as
// STATUS_FULL_TIME, which is every group match and any knockout decided in 90.
// A match completed in any other state (extra time / penalties) returns nil, so
// the admin enters the 90' score by hand rather than us recording an ET result.
//
// Verify ESPN's status name for an ET/penalty finish with `check-feed` once
// knockouts play (July) and extend this if it differs from the assumption.
func reg90(statusName, homeScore, awayScore string) *results.Score {
	if statusName != "STATUS_FULL_TIME" {
		return nil
	}
	h, err := strconv.Atoi(homeScore)
	if err != nil {
		return nil
	}
	a, err := strconv.Atoi(awayScore)
	if err != nil {
		return nil
	}
	return &results.Score{A: h, B: a}
}
