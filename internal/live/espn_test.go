package live

import (
	"strings"
	"testing"
)

// sampleScoreboard mirrors the real fifa.world payload shape (verified against
// ESPN): one finished match, one in-play, one scheduled.
const sampleScoreboard = `{
  "events": [
    {
      "date": "2026-06-12T19:00Z",
      "competitions": [{
        "competitors": [
          {"homeAway": "home", "score": "1", "team": {"displayName": "Canada"}},
          {"homeAway": "away", "score": "1", "team": {"displayName": "Bosnia-Herzegovina"}}
        ],
        "status": {"displayClock": "90'+7'", "period": 2, "type": {"state": "post"}}
      }]
    },
    {
      "date": "2026-06-12T22:00Z",
      "competitions": [{
        "competitors": [
          {"homeAway": "home", "score": "2", "team": {"displayName": "Brazil"}},
          {"homeAway": "away", "score": "0", "team": {"displayName": "Serbia"}}
        ],
        "status": {"displayClock": "67'", "period": 2, "type": {"state": "in"}}
      }]
    },
    {
      "date": "2026-06-13T01:00Z",
      "competitions": [{
        "competitors": [
          {"homeAway": "home", "score": "0", "team": {"displayName": "United States"}},
          {"homeAway": "away", "score": "0", "team": {"displayName": "Paraguay"}}
        ],
        "status": {"displayClock": "0'", "period": 0, "type": {"state": "pre"}}
      }]
    }
  ]
}`

func TestDecodeEvents(t *testing.T) {
	evs, err := decodeEvents(strings.NewReader(sampleScoreboard))
	if err != nil {
		t.Fatalf("decodeEvents: %v", err)
	}
	if len(evs) != 3 {
		t.Fatalf("got %d events, want 3", len(evs))
	}

	// Finished match.
	if evs[0].Home != "Canada" || evs[0].HomeScore != 1 || evs[0].AwayScore != 1 || evs[0].State != StatePost {
		t.Errorf("event 0 wrong: %+v", evs[0])
	}
	// In-play with score and clock.
	if evs[1].Home != "Brazil" || evs[1].HomeScore != 2 || evs[1].State != StateIn || evs[1].Clock != "67'" {
		t.Errorf("event 1 wrong: %+v", evs[1])
	}
	// Scheduled.
	if evs[2].State != StatePre {
		t.Errorf("event 2 state = %v, want pre", evs[2].State)
	}
	// Date parsed to UTC.
	if evs[0].Date.IsZero() || evs[0].Date.Hour() != 19 {
		t.Errorf("event 0 date not parsed: %v", evs[0].Date)
	}
}

func TestDecodeEventsSanitizesClock(t *testing.T) {
	// The feed is untrusted and the clock renders into every player's terminal.
	// An ANSI/control-char payload must be neutralized (no ESC, no letters).
	const evil = `{"events":[{"date":"2026-06-12T19:00Z","competitions":[{` +
		`"competitors":[{"homeAway":"home","score":"1","team":{"displayName":"A"}},` +
		`{"homeAway":"away","score":"0","team":{"displayName":"B"}}],` +
		`"status":{"displayClock":"\u001b[31mHACK\u001b[2J67'","period":2,"type":{"state":"in"}}}]}]}`
	evs, err := decodeEvents(strings.NewReader(evil))
	if err != nil {
		t.Fatalf("decodeEvents: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1", len(evs))
	}
	clock := evs[0].Clock
	if strings.ContainsRune(clock, '\x1b') || strings.ContainsAny(clock, "HACKJmh[") {
		t.Errorf("clock not sanitized: %q", clock)
	}
	// The benign digits/quote survive.
	if !strings.Contains(clock, "67'") {
		t.Errorf("sanitized clock lost legit content: %q", clock)
	}
}

func TestDecodeEventsRejectsBadScores(t *testing.T) {
	for _, score := range []string{"-3", "150"} {
		body := `{"events":[{"date":"2026-06-12T19:00Z","competitions":[{` +
			`"competitors":[{"homeAway":"home","score":"` + score + `","team":{"displayName":"A"}},` +
			`{"homeAway":"away","score":"0","team":{"displayName":"B"}}],` +
			`"status":{"type":{"state":"in"}}}]}]}`
		evs, err := decodeEvents(strings.NewReader(body))
		if err != nil {
			t.Fatalf("decodeEvents(%s): %v", score, err)
		}
		if len(evs) != 0 {
			t.Errorf("score %s should be rejected, got %+v", score, evs)
		}
	}
}

func TestDecodeEventsSkipsMalformed(t *testing.T) {
	// An event missing a competitor must be skipped, not panic.
	const bad = `{"events":[{"date":"2026-06-12T19:00Z","competitions":[{"competitors":[{"homeAway":"home","score":"1","team":{"displayName":"Solo"}}],"status":{"type":{"state":"in"}}}]}]}`
	evs, err := decodeEvents(strings.NewReader(bad))
	if err != nil {
		t.Fatalf("decodeEvents: %v", err)
	}
	if len(evs) != 0 {
		t.Errorf("malformed event should be skipped, got %+v", evs)
	}
}
