// Package footballdata implements results.Fetcher against the football-data.org
// v4 API. It is the only place in the results path that does HTTP; it maps the
// provider's JSON into the I/O-free results.FeedMatch type.
package footballdata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"bethoven/internal/results"
)

const defaultBaseURL = "https://api.football-data.org/v4"

// Client fetches a competition's matches from football-data.org.
type Client struct {
	apiKey      string
	competition string // competition code, e.g. "WC"
	baseURL     string
	http        *http.Client
}

// New builds a client. A blank baseURL uses the production API; tests pass a
// local test-server URL.
func New(apiKey, competition, baseURL string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		apiKey:      apiKey,
		competition: competition,
		baseURL:     baseURL,
		http:        &http.Client{Timeout: 15 * time.Second},
	}
}

// Fetch retrieves the competition's matches and maps them to feed matches.
func (c *Client) Fetch(ctx context.Context) ([]results.FeedMatch, error) {
	url := fmt.Sprintf("%s/competitions/%s/matches", c.baseURL, c.competition)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("football-data: GET %s: status %d", url, resp.StatusCode)
	}

	var payload matchesResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("football-data: decode: %w", err)
	}
	return payload.toFeed()
}

// --- JSON shapes (subset of the v4 /competitions/{code}/matches response) ---

type matchesResponse struct {
	Matches []apiMatch `json:"matches"`
}

type apiMatch struct {
	ID       int64    `json:"id"`
	UTCDate  string   `json:"utcDate"`
	Status   string   `json:"status"` // SCHEDULED, TIMED, IN_PLAY, PAUSED, FINISHED, ...
	HomeTeam apiTeam  `json:"homeTeam"`
	AwayTeam apiTeam  `json:"awayTeam"`
	Score    apiScore `json:"score"`
}

type apiTeam struct {
	Name string `json:"name"`
}

type apiScore struct {
	Duration string       `json:"duration"` // REGULAR, EXTRA_TIME, PENALTY_SHOOTOUT
	FullTime apiScoreLine `json:"fullTime"`
	// RegularTime, when present, is the score after 90 minutes — the figure
	// BEThoven scores on, even for knockouts that went to extra time. Not every
	// provider response includes it; toReg90 falls back safely when it's absent.
	RegularTime *apiScoreLine `json:"regularTime"`
}

type apiScoreLine struct {
	Home *int `json:"home"`
	Away *int `json:"away"`
}

func (r matchesResponse) toFeed() ([]results.FeedMatch, error) {
	out := make([]results.FeedMatch, 0, len(r.Matches))
	for _, m := range r.Matches {
		kickoff, err := time.Parse(time.RFC3339, m.UTCDate)
		if err != nil {
			return nil, fmt.Errorf("match %d: bad utcDate %q: %w", m.ID, m.UTCDate, err)
		}
		fm := results.FeedMatch{
			ExternalRef: strconv.FormatInt(m.ID, 10),
			HomeTeam:    m.HomeTeam.Name,
			AwayTeam:    m.AwayTeam.Name,
			KickoffUTC:  kickoff.UTC(),
			Finished:    m.Status == "FINISHED",
		}
		if fm.Finished {
			fm.Reg90 = m.Score.toReg90()
		}
		out = append(out, fm)
	}
	return out, nil
}

// toReg90 extracts the regulation 90-minute score, or nil if it can't be
// determined safely.
//
// IMPORTANT (verify before launch): for a knockout that went to extra time we
// must store the 90' score, not the post-ET score. When the feed supplies
// regularTime we use it directly. When it doesn't, we trust fullTime only if the
// match ended in regulation (duration == REGULAR — true for every group match);
// an ET/penalty match without a regulation-time breakdown returns nil so the
// admin enters the 90' score by hand rather than us recording an ET result.
func (s apiScore) toReg90() *results.Score {
	if s.RegularTime != nil && s.RegularTime.Home != nil && s.RegularTime.Away != nil {
		return &results.Score{A: *s.RegularTime.Home, B: *s.RegularTime.Away}
	}
	if s.Duration == "REGULAR" && s.FullTime.Home != nil && s.FullTime.Away != nil {
		return &results.Score{A: *s.FullTime.Home, B: *s.FullTime.Away}
	}
	return nil
}
