# ESPN feed reference (BEThoven live scores)

BEThoven's optional live feed (`internal/live`) is powered by ESPN's **unofficial,
keyless** site API. No token, signup, or `Authorization` header — just HTTP GET
returning JSON. It is **undocumented and can change without notice**, which is
exactly why it sits behind the `live.Provider` interface (`ESPNProvider`) and why
everything it returns is treated as **untrusted input**.

This file documents the endpoints, the response shapes, what we consume today, and
what's available for the future. It's a map for the next person who wants to enrich
the live experience — not an API contract ESPN owes us.

---

## Base URL & league slug

```
https://site.api.espn.com/apis/site/v2/sports/soccer/{league}
```

- **`{league}`** — `fifa.world` for the men's World Cup (`live.DefaultLeague`).
  Other slugs follow the same shape (`eng.1`, `usa.1`, `uefa.champions`, …).
- Constant: `espnBase` + `DefaultLeague` in `internal/live/espn.go`.

There is a parallel **`sports.core.api.espn.com`** ("core") host with richer,
hyperlinked (`$ref`) resources. We do **not** use it — the `site.api` host returns
everything we need in one denormalized payload, which is simpler and cheaper.

---

## Endpoints we use

### 1. Scoreboard (the poll) — `GET /scoreboard?dates=YYYYMMDD`

```
.../soccer/fifa.world/scoreboard?dates=20260619
```

Returns every fixture for that UTC day with score, status, odds, and a curated
`details` array. **This is the one the `Poller` hits every cycle** (default 1m),
for today and yesterday (`poller.go` fetches `now` and `now-1d` so a match ending
near a restart or midnight UTC still finalizes). ~55 KB for a 4-match day.

Shape we parse (`espnResp` / `decodeEvents` in `espn.go`):

```jsonc
{
  "events": [{
    "id": "760442",                         // ← used to fetch the summary (key events)
    "date": "2026-06-19T19:00Z",            // kickoff, minute precision (no seconds)
    "shortName": "AUS @ USA",
    "status": { "type": { "state": "in", "shortDetail": "44'" } },
    "competitions": [{
      "competitors": [
        { "homeAway": "home", "score": "1", "team": { "displayName": "United States" },
          "form": "WLWLL", "records": [{ "type": "total", "summary": "1-0-0" }],
          "statistics": [ { "name": "possessionPct", "displayValue": "71.4" }, … ] },
        { "homeAway": "away", "score": "0", "team": { "displayName": "Australia" }, … }
      ],
      "status": { "displayClock": "44'", "period": 2,
                   "type": { "state": "in", "name": "STATUS_HALFTIME", "shortDetail": "HT" } },
      "details": [                          // curated key plays (see "athlete refs" caveat)
        { "type": { "text": "Own Goal" }, "clock": { "displayValue": "11'" },
          "scoringPlay": true, "team": { "id": "660" }, "athletesInvolved": [] }
      ],
      "odds": [
        { "details": "USA -180", "overUnder": 2.5,
          "provider": { "priority": 1, "name": "DraftKings" } }
      ]
    }]
  }]
}
```

| Field | We use it for |
|---|---|
| `events[].id` | key to fetch the summary endpoint |
| `events[].date` | kickoff, for the resolver's date guard (`parseESPNDate`) |
| `competitors[].team.displayName` | team-pair resolution to a stored match (never rendered) |
| `competitors[].score` | live score (validated 0–99, `validScore`) |
| `competitors[].homeAway` | orient home/away to our TeamA/TeamB |
| `status.displayClock` | display clock (sanitized, `cleanClock`) |
| `status.period` | `LiveMinute` (half number, not a wall-clock minute) |
| `status.type.state` | `pre`/`in`/`post` → `live.State` (`ParseState`) |
| `status.type.name` | finer in-play phase → `live.Phase` (`ParsePhase`) |
| `odds[]` (priority-1 provider) | `LiveOdds` string (sanitized, `cleanOdds`) |

**Status states (`status.type.state`)** — the coarse bucket: `pre` (never revealed —
preserves blind betting), `in` (cached + folded into provisional points), `post`
(auto-finalized via `FinalizeFromFeed`, only if not already finished — admin entry
always wins).

**Phase (`status.type.name`)** — the finer breakdown *within* `in`. The match is
still `state:"in"` at the interval, so the coarse state can't tell you it's paused;
`status.type.name` can. We map a few via `ParsePhase` into OUR controlled labels
(never raw feed text): `STATUS_HALFTIME*` → `halftime`, `STATUS_*EXTRA*` →
`extra_time`, `STATUS_PENALTIES`/`STATUS_SHOOTOUT` → `penalties`; anything else ⇒ ""
(ordinary live play). Carried as `Score.Phase`/`Match.LivePhase`. Two uses: the TUI
shows **"HT"** instead of the stale stoppage clock (`45'+8'`) at halftime, and the
phase is fed to BETanIA so the live line reacts to *the break*, not as if the ball
is rolling. Other `name` values seen: `STATUS_SCHEDULED`, `STATUS_FIRST_HALF`,
`STATUS_IN_PROGRESS`, `STATUS_FULL_TIME`/`STATUS_FINAL`.

### 2. Summary (per match) — `GET /summary?event={id}`

```
.../soccer/fifa.world/summary?event=760442
```

The deep payload for **one** match (~350 KB). We fetch it **only for in-play
events** (a handful at a time), once per poll, and read **only `keyEvents`** today
(`summaryResp` / `decodeKeyEvents`). Failures are tolerated per-event (the match
just carries no events that pass).

```jsonc
{
  "keyEvents": [
    { "type": { "text": "Kickoff" }, "text": "First Half begins.",
      "clock": { "displayValue": "" }, "scoringPlay": false },
    { "type": { "text": "Own Goal" },
      "text": "Own Goal by Cameron Burgess, Australia. USA 1, Australia 0.",
      "clock": { "displayValue": "11'" }, "scoringPlay": true },
    { "type": { "text": "Yellow Card" },
      "text": "Jordan Bos (Australia) is shown the yellow card for a bad foul.",
      "clock": { "displayValue": "16'" }, "scoringPlay": false }
  ],
  "commentary": [ { "time": { "displayValue": "1'" },
      "text": "Attempt saved. Mohamed Touré (Australia) right footed shot…" }, … ],
  "rosters":   [ { "team": {…}, "roster": [ { "athlete": {…}, "position": {…},
                   "starter": true, "formationPlace": "1" }, … ] }, … ],
  "boxscore":  { "teams": [ { "team": {…}, "statistics": [
                   { "name": "possessionPct", "displayValue": "71.4" },
                   { "name": "shotsOnTarget", "displayValue": "0" },
                   { "name": "wonCorners", "displayValue": "2" }, … ] } ] },
  "gameInfo":  { "venue": { "fullName": "Lumen Field",
                   "address": { "city": "Seattle, Washington", "country": "USA" } },
                 "attendance": null,
                 "officials": [ { "displayName": "Felix Zwayer",
                   "position": { "displayName": "Referee" } } ] },
  "headToHeadGames": [ … ], "lastFiveGames": [ … ],
  "standings": { "groups": [ … ] }, "pickcenter": [ { … odds … } ],
  "leaders": [ … ], "news": { … }, "videos": [ … ]
}
```

`keyEvents[]` fields we read: `type.text` (kind), `text` (human description **with
the player name**), `clock.displayValue` (minute), `scoringPlay` (goal flag). We
keep the most recent `maxKeyEvents` (12), drop empty-text markers, sanitize every
string, and surface them as `models.MatchEvent` → `LiveSituation` so BETanIA can
name the scorer.

---

## ⚠️ Caveat: structured athlete refs are empty for `fifa.world`

In **both** the scoreboard `details[]` and the summary `keyEvents[]`,
`athletesInvolved` comes back **empty** for this competition — there is no clean
`athlete.id`/`athlete.displayName` to read. The scorer's name lives **inside the
`text` string** (`"Own Goal by Cameron Burgess…"`). That's why we pass the prose
through (sanitized) rather than reconstructing "Scorer (min')" from fields. If a
future tournament populates the refs, prefer them — they need no NLP and no prose
sanitization.

---

## Security: the feed is UNTRUSTED

Everything ESPN returns is attacker-influenceable in principle and **renders into
players' terminals and into BETanIA's prompt** (the same ANSI-injection boundary as
display names and model output). Three sanitizers in `espn.go`, all **strip — don't
reject** (we can't ask ESPN to fix its data):

- **`cleanClock`** — digit/clock-punctuation whitelist, 12-char cap. For the display
  clock.
- **`cleanOdds`** — letters+digits+small-punctuation whitelist (team abbreviations
  like "USA"), 32-char cap. For the odds string.
- **`cleanEventText`** — prose sanitizer (mirrors `ai.sanitizeText`): consumes whole
  CSI/escape sequences, drops C0/C1 controls and non-printables, collapses
  whitespace, length-caps (200 for text, 40 for type). For key-event descriptions,
  which are free-form prose carrying names.

Scores are validated to 0–99 (`validScore`). **Feed team names are used only for
resolution, never rendered.**

---

## Resolution & caching (how a feed event becomes a live score)

1. **Resolve** each event to a stored match by canonical **team-pair** + a ±36h
   kickoff-date guard (`poller.resolve`). Names are accent-folded + normalized
   (`normalize`/`foldAccents`) and run through an **alias map** (`defaultAliases`,
   e.g. `unitedstates→usa`, `turkey→turkiye`). The durable fix for a mismatch is to
   align `fixtures.json` names with ESPN's; the alias map covers residue.
2. **Cache** in-play scores in memory (`live.Cache`, the `service.LiveStore` port).
   The map is **rebuilt every poll**, so a match the feed stops reporting drops out
   instead of lingering as a phantom in-play score. **Nothing is persisted** — a
   restart re-fetches within one cycle.
3. **Finalize** on a `post` event via `FinalizeFromFeed`, **only if not already
   finished** (admin `EnterResult` always overrides).

Disable the whole feed with `BETHOVEN_LIVE_ENABLED=false` (a nil `LiveStore` ⇒
behaviour identical to no feed).

---

## What we consume vs. what's there

**Consumed today:** score, clock, period, state, odds (scoreboard) + key events
(summary).

**Available, not yet used** — high-signal candidates for the future:

| Data | Where | Possible use |
|---|---|---|
| Full play-by-play `commentary[]` | summary | richer live lines (verbose; 46+ entries/match) |
| Team `statistics` (possession, shots, corners, fouls, saves) | scoreboard `competitors[].statistics` **and** summary `boxscore.teams[].statistics` | "USA 71% possession but 0 shots on target" colour |
| `form` ("WLWLL") + `records` ("1-0-0") | scoreboard `competitors[]` | pre-match framing |
| Lineups / formations `rosters[]` | summary | "Player X starts on the bench" |
| `gameInfo` (venue, **referee**, attendance) | summary | scene-setting |
| `headToHeadGames` / `lastFiveGames` | summary | rivalry/history context |
| `standings.groups` | summary | group-table awareness |
| `pickcenter` / `odds` history | both | richer odds narrative |

Any new field that **renders or reaches the model** must go through a sanitizer
(`cleanEventText` for prose) and, if it's a count/score, a range check.

---

## Operational notes & gotchas

- **Schema drift is expected.** ESPN can rename/restructure without notice. Decoders
  are defensive: missing fields decode to zero values, malformed events are skipped
  (not fatal), and a per-day fetch failure is tolerated (`Fetch` continues).
- **Extra cost of summary.** Each in-play match adds one ~350 KB GET per poll. Fine
  for a World Cup slate (a handful live at once); revisit if you widen the league set
  or shorten the interval.
- **Dates are minute-precision** (`2026-06-12T19:00Z`) — not RFC3339. `parseESPNDate`
  tries that layout first, then RFC3339, then a zero time (the resolver tolerates
  zero by matching on teams).
- **`attendance` is often `null`/`0`** pre/early match; `venue.fullName` can be
  absent on the scoreboard (present on the summary).
- **Test without the network.** `decodeEvents` / `decodeKeyEvents` take an
  `io.Reader`, so they're unit-tested against canned payloads (`espn_test.go`) —
  including ANSI-injection cases. Mirror that pattern for any new decoder.

---

## Quick manual probe

```sh
# today's fixtures (scoreboard)
curl -s "https://site.api.espn.com/apis/site/v2/sports/soccer/fifa.world/scoreboard?dates=$(date -u +%Y%m%d)" | jq '.events[] | {id, name: .shortName, state: .status.type.state}'

# one match's key events (summary) — use an id from above
curl -s "https://site.api.espn.com/apis/site/v2/sports/soccer/fifa.world/summary?event=760442" | jq '.keyEvents[] | {clock: .clock.displayValue, type: .type.text, text}'
```
