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
        "status": {"displayClock": "67'", "period": 2, "type": {"state": "in"}},
        "odds": [
          {"details": "Serbia +320", "overUnder": 3.0, "provider": {"priority": 2, "name": "Secondary"}},
          {"details": "Brazil -160", "overUnder": 2.5, "provider": {"priority": 1, "name": "DraftKings"}}
        ]
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
	// Odds: the priority-1 provider wins over the secondary entry.
	if evs[1].Odds != "Brazil -160 · O/U 2.5" {
		t.Errorf("event 1 odds = %q, want %q", evs[1].Odds, "Brazil -160 · O/U 2.5")
	}
	// No odds array -> empty string, gracefully.
	if evs[0].Odds != "" {
		t.Errorf("event 0 odds = %q, want empty", evs[0].Odds)
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

func TestDecodeEventsSanitizesOdds(t *testing.T) {
	// Odds details are UNTRUSTED feed text rendered into terminals (and later an
	// AI comment), so an ANSI/control payload must be neutralized. Letters are
	// allowed (team abbreviations like "USA") but ESC sequences and the CSI
	// bracket are stripped — strip-don't-reject, like cleanClock.
	const evil = `{"events":[{"date":"2026-06-12T19:00Z","competitions":[{` +
		`"competitors":[{"homeAway":"home","score":"1","team":{"displayName":"A"}},` +
		`{"homeAway":"away","score":"0","team":{"displayName":"B"}}],` +
		`"status":{"type":{"state":"in"}},` +
		`"odds":[{"details":"\u001b[31mUSA -160\u001b[2J","overUnder":2.5,"provider":{"priority":1,"name":"DK"}}]}]}]}`
	evs, err := decodeEvents(strings.NewReader(evil))
	if err != nil {
		t.Fatalf("decodeEvents: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1", len(evs))
	}
	odds := evs[0].Odds
	// The ESC byte and the CSI bracket must be gone; benign letters/digits survive.
	if strings.ContainsRune(odds, '\x1b') || strings.ContainsRune(odds, '[') {
		t.Errorf("odds not sanitized: %q", odds)
	}
	if !strings.Contains(odds, "USA -160") || !strings.Contains(odds, "O/U 2.5") {
		t.Errorf("sanitized odds lost legit content: %q", odds)
	}
}

// sampleSummary mirrors the real fifa.world summary payload shape: a kickoff
// marker (no minute), an own goal, a yellow card, and an empty delay marker.
const sampleSummary = `{
  "keyEvents": [
    {"type": {"text": "Kickoff"}, "text": "First Half begins.", "clock": {"displayValue": "0'"}, "scoringPlay": false},
    {"type": {"text": "Own Goal"}, "text": "Own Goal by Cameron Burgess, Australia. USA 1, Australia 0.", "clock": {"displayValue": "11'"}, "scoringPlay": true},
    {"type": {"text": "Yellow Card"}, "text": "Jordan Bos (Australia) is shown the yellow card for a bad foul.", "clock": {"displayValue": "16'"}, "scoringPlay": false},
    {"type": {"text": "Start Delay"}, "text": "", "clock": {"displayValue": "25'"}, "scoringPlay": false}
  ]
}`

func TestDecodeKeyEvents(t *testing.T) {
	kes, err := decodeKeyEvents(strings.NewReader(sampleSummary))
	if err != nil {
		t.Fatalf("decodeKeyEvents: %v", err)
	}
	// The empty-text delay marker is dropped; the other three survive.
	if len(kes) != 3 {
		t.Fatalf("got %d key events, want 3: %+v", len(kes), kes)
	}
	og := kes[1]
	if og.Type != "Own Goal" || og.Clock != "11'" || !og.Scoring {
		t.Errorf("own goal wrong: %+v", og)
	}
	if !strings.Contains(og.Text, "Cameron Burgess") {
		t.Errorf("own goal text lost the scorer: %q", og.Text)
	}
	if kes[2].Type != "Yellow Card" || kes[2].Scoring {
		t.Errorf("yellow card wrong: %+v", kes[2])
	}
}

func TestDecodeKeyEventsSanitizes(t *testing.T) {
	// Key-event text is UNTRUSTED feed prose rendered into terminals and BETanIA's
	// prompt, so an ANSI/control payload must be neutralized (strip, keep prose).
	const evil = `{"keyEvents":[{"type":{"text":"Goal"},` +
		`"text":"\u001b[31mGoal! \u001b[2JLionel Messi scores.","clock":{"displayValue":"\u001b[1m45'"},"scoringPlay":true}]}`
	kes, err := decodeKeyEvents(strings.NewReader(evil))
	if err != nil {
		t.Fatalf("decodeKeyEvents: %v", err)
	}
	if len(kes) != 1 {
		t.Fatalf("got %d, want 1", len(kes))
	}
	got := kes[0]
	if strings.ContainsRune(got.Text, '\x1b') || strings.ContainsAny(got.Text, "[") {
		t.Errorf("event text not sanitized: %q", got.Text)
	}
	if !strings.Contains(got.Text, "Lionel Messi scores") {
		t.Errorf("sanitized text lost legit content: %q", got.Text)
	}
	if strings.ContainsRune(got.Clock, '\x1b') || !strings.Contains(got.Clock, "45'") {
		t.Errorf("clock not sanitized/kept: %q", got.Clock)
	}
}

func TestCleanEventTextTruncates(t *testing.T) {
	long := strings.Repeat("a", maxEventTextLen+50)
	got := cleanEventText(long, maxEventTextLen)
	if len([]rune(got)) != maxEventTextLen {
		t.Errorf("len = %d, want %d", len([]rune(got)), maxEventTextLen)
	}
}

func TestParsePhase(t *testing.T) {
	cases := []struct {
		name, shortDetail, want string
	}{
		{"STATUS_HALFTIME", "HT", PhaseHalftime},
		{"STATUS_HALFTIME_ET", "HT", PhaseHalftime},
		{"STATUS_FIRST_HALF", "1st Half", ""},
		{"STATUS_IN_PROGRESS", "67'", ""},
		{"STATUS_PENALTIES", "PENs", PhasePenalties},
		{"STATUS_SHOOTOUT", "", PhasePenalties},
		{"STATUS_SECOND_EXTRA_TIME", "ET", PhaseExtraTime},
		// The key case: at end of ET the name still says EXTRA, but shortDetail
		// "AET-pens" tells us it's going to a shootout → penalties wins over extra.
		{"STATUS_END_OF_EXTRATIME", "AET-pens", PhasePenalties},
		{"STATUS_FULL_TIME", "FT-pens", PhasePenalties}, // a comp that skips ET
		{"", "", ""},
	}
	for _, c := range cases {
		if got := ParsePhase(c.name, c.shortDetail); got != c.want {
			t.Errorf("ParsePhase(%q, %q) = %q, want %q", c.name, c.shortDetail, got, c.want)
		}
	}
}

func TestDecodeEventsParsesHalftime(t *testing.T) {
	// An in-play match at the interval: state stays "in", but the type name marks
	// halftime, which must surface as PhaseHalftime.
	const body = `{"events":[{"date":"2026-06-12T19:00Z","competitions":[{` +
		`"competitors":[{"homeAway":"home","score":"2","team":{"displayName":"USA"}},` +
		`{"homeAway":"away","score":"0","team":{"displayName":"Australia"}}],` +
		`"status":{"displayClock":"45'+8'","period":1,"type":{"state":"in","name":"STATUS_HALFTIME"}}}]}]}`
	evs, err := decodeEvents(strings.NewReader(body))
	if err != nil {
		t.Fatalf("decodeEvents: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1", len(evs))
	}
	if evs[0].State != StateIn {
		t.Errorf("state = %v, want in", evs[0].State)
	}
	if evs[0].Phase != PhaseHalftime {
		t.Errorf("phase = %q, want %q", evs[0].Phase, PhaseHalftime)
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
