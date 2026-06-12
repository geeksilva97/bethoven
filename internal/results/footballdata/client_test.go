package footballdata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// sampleResponse mirrors the subset of football-data.org's v4
// /competitions/{code}/matches payload that we decode: a finished group match,
// a finished knockout with a regulation-time breakdown, a finished knockout
// without one (extra time), and a match still in play.
const sampleResponse = `{
  "matches": [
    {
      "id": 1001,
      "utcDate": "2026-06-13T22:00:00Z",
      "status": "FINISHED",
      "homeTeam": {"name": "Brazil"},
      "awayTeam": {"name": "Morocco"},
      "score": {"duration": "REGULAR", "fullTime": {"home": 2, "away": 1}}
    },
    {
      "id": 1002,
      "utcDate": "2026-07-05T19:00:00Z",
      "status": "FINISHED",
      "homeTeam": {"name": "Spain"},
      "awayTeam": {"name": "France"},
      "score": {"duration": "EXTRA_TIME", "fullTime": {"home": 2, "away": 1}, "regularTime": {"home": 1, "away": 1}}
    },
    {
      "id": 1003,
      "utcDate": "2026-07-06T19:00:00Z",
      "status": "FINISHED",
      "homeTeam": {"name": "Argentina"},
      "awayTeam": {"name": "Portugal"},
      "score": {"duration": "PENALTY_SHOOTOUT", "fullTime": {"home": 1, "away": 1}}
    },
    {
      "id": 1004,
      "utcDate": "2026-06-20T19:00:00Z",
      "status": "IN_PLAY",
      "homeTeam": {"name": "Germany"},
      "awayTeam": {"name": "Japan"},
      "score": {"duration": "REGULAR", "fullTime": {"home": null, "away": null}}
    }
  ]
}`

func TestFetchDecodesAndExtractsReg90(t *testing.T) {
	var gotPath, gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-Auth-Token")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleResponse))
	}))
	defer srv.Close()

	c := New("test-token", "WC", srv.URL)
	feed, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if gotPath != "/competitions/WC/matches" {
		t.Errorf("request path = %q, want /competitions/WC/matches", gotPath)
	}
	if gotToken != "test-token" {
		t.Errorf("auth token = %q, want test-token", gotToken)
	}
	if len(feed) != 4 {
		t.Fatalf("expected 4 feed matches, got %d", len(feed))
	}

	// 1: regulation group match -> Reg90 from fullTime.
	if m := feed[0]; m.ExternalRef != "1001" || !m.Finished || m.Reg90 == nil || m.Reg90.A != 2 || m.Reg90.B != 1 {
		t.Errorf("group match decoded wrong: %+v (reg90=%+v)", m, m.Reg90)
	}
	if feed[0].KickoffUTC.IsZero() {
		t.Errorf("kickoff not parsed for match 1001")
	}

	// 2: ET with regularTime -> Reg90 is the 90' score (1-1), NOT the 2-1 result.
	if m := feed[1]; m.Reg90 == nil || m.Reg90.A != 1 || m.Reg90.B != 1 {
		t.Errorf("ET match should use regulation-time score 1-1, got %+v", m.Reg90)
	}

	// 3: ET/penalties without a regulation-time breakdown -> nil (left for admin).
	if m := feed[2]; !m.Finished || m.Reg90 != nil {
		t.Errorf("ET match without regularTime should yield nil Reg90, got %+v", m.Reg90)
	}

	// 4: in play -> not finished, no score.
	if m := feed[3]; m.Finished || m.Reg90 != nil {
		t.Errorf("in-play match should be unfinished with nil Reg90, got %+v", m)
	}
}

func TestFetchNon200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden) // e.g. bad/missing token
	}))
	defer srv.Close()

	c := New("bad", "WC", srv.URL)
	if _, err := c.Fetch(context.Background()); err == nil {
		t.Fatal("expected an error on non-200 response")
	}
}
