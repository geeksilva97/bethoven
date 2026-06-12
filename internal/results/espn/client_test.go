package espn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// sampleResponse mirrors the subset of ESPN's soccer scoreboard payload that we
// decode: a finished group match (regulation), a finished knockout that went to
// extra time (final score is post-ET, so we must NOT trust it as the 90'
// score), and a match still in play. Scores are strings and dates omit seconds,
// exactly as the live feed returns them.
const sampleResponse = `{
  "events": [
    {
      "id": "760415",
      "date": "2026-06-11T19:00Z",
      "competitions": [{
        "status": {"type": {"name": "STATUS_FULL_TIME", "completed": true}},
        "competitors": [
          {"homeAway": "home", "score": "2", "team": {"displayName": "Mexico"}},
          {"homeAway": "away", "score": "0", "team": {"displayName": "South Africa"}}
        ]
      }]
    },
    {
      "id": "770001",
      "date": "2026-07-05T19:00Z",
      "competitions": [{
        "status": {"type": {"name": "STATUS_FINAL_PEN", "completed": true}},
        "competitors": [
          {"homeAway": "home", "score": "2", "team": {"displayName": "Spain"}},
          {"homeAway": "away", "score": "2", "team": {"displayName": "France"}}
        ]
      }]
    },
    {
      "id": "760416",
      "date": "2026-06-12T19:00Z",
      "competitions": [{
        "status": {"type": {"name": "STATUS_SECOND_HALF", "completed": false}},
        "competitors": [
          {"homeAway": "home", "score": "1", "team": {"displayName": "Canada"}},
          {"homeAway": "away", "score": "1", "team": {"displayName": "Bosnia-Herzegovina"}}
        ]
      }]
    }
  ]
}`

func TestFetchDecodesAndExtractsReg90(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleResponse))
	}))
	defer srv.Close()

	c := New("fifa.world", srv.URL)
	feed, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if gotPath != "/fifa.world/scoreboard" {
		t.Errorf("request path = %q, want /fifa.world/scoreboard", gotPath)
	}
	if gotQuery != "dates=20260611-20260719&limit=500" {
		t.Errorf("query = %q, want the WC date range + limit", gotQuery)
	}
	if len(feed) != 3 {
		t.Fatalf("expected 3 feed matches, got %d", len(feed))
	}

	// 1: finished regulation group match -> Reg90 from the final score, home=A.
	if m := feed[0]; m.ExternalRef != "760415" || !m.Finished || m.HomeTeam != "Mexico" ||
		m.Reg90 == nil || m.Reg90.A != 2 || m.Reg90.B != 0 {
		t.Errorf("group match decoded wrong: %+v (reg90=%+v)", m, m.Reg90)
	}
	if feed[0].KickoffUTC.IsZero() {
		t.Errorf("kickoff not parsed for match 760415")
	}

	// 2: finished after penalties -> final score is post-90', so Reg90 is nil and
	// the match is left for the admin (we never record an ET/penalty score as 90').
	if m := feed[1]; !m.Finished || m.Reg90 != nil {
		t.Errorf("penalty match should yield nil Reg90 (left for admin), got %+v", m.Reg90)
	}

	// 3: in play -> not finished, no score.
	if m := feed[2]; m.Finished || m.Reg90 != nil {
		t.Errorf("in-play match should be unfinished with nil Reg90, got %+v", m)
	}
}

func TestFetchNon200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New("fifa.world", srv.URL)
	if _, err := c.Fetch(context.Background()); err == nil {
		t.Fatal("expected an error on non-200 response")
	}
}
