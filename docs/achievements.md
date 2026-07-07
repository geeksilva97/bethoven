# Achievements ("Trophy Room") — spec

Badges players earn from their betting history: some are **superlatives** (one
holder in the pool — "most exact scores"), some are **thresholds** (anyone who
qualifies — "a perfect round"). Shown in a new Trophy Room screen, on the
end-of-tournament player card, and fed to BETanIA as roast material.

## Principles (all inherited from existing patterns)

- **Pure core, service plumbing.** A new `internal/achievements` package mirrors
  `internal/scoring`: pure functions over an input struct, never reads the DB,
  heavily table-tested. The **service** builds the input (like `scoring.Pool`).
- **Computed at read time, nothing stored.** Like `StandingsHistory` and the
  card stats — a pure fold of persisted bets + results. No schema change, no
  migration, editing a past result re-derives everything. Badges can change
  hands mid-tournament; that's a feature (BETanIA fodder: "you just lost the
  Oracle to Edy").
- **Finished matches only.** Every criterion reads only `m.Finished` matches, so
  nothing leaks a pick before kickoff — same reveal boundary as
  `MatchLeaderboard` / `public_bets`. The Trophy Room is therefore **ungated**
  (any player), like `AllLeaderboardComments`: badges are shared fun; individual
  upcoming picks stay private.
- **Active scoring mode applies.** Point-based criteria go through the same
  `scorer.points` the leaderboard uses, so badges agree with the board in
  Classic / Proximity / Scarcity.

## Badge catalog

### Superlatives — one holder; **ties share the badge**; each has a minimum so a
### 2-bet fluke can't claim it (same spirit as `scarcityQuorum`)

| Badge | Emoji | Criterion (finished matches only) | Minimum to hold |
|---|---|---|---|
| The Oracle | 🔮 | most exact scores | ≥ 2 exacts |
| Longest Streak | 🔥 | longest run of consecutive correct-result picks (skip or miss breaks it — reuse the `BestStreak` rule) | ≥ 3 |
| Top Round | 💥 | most points gained in a single round (`PointsGained` from `StandingsHistory`) | > 0 |
| The Comeback | 🧗 | biggest single-round climb (`Movement`, first-joined-round excluded, as in cards) | ≥ 2 places |
| Free Fall | 🪂 | biggest single-round drop | ≥ 2 places |
| Draw Whisperer | 🤝 | most correctly-called draws | ≥ 2 |
| Goal Merchant | ⚽ | highest average predicted goals per pick | ≥ 5 picks |
| The Accountant | 🧾 | lowest average predicted goals per pick | ≥ 5 picks |
| Deadline Junkie | ⏱ | most picks placed < 10 min before kickoff | ≥ 3 such picks |
| Early Bird | 🌅 | most picks placed > 48 h before kickoff | ≥ 3 such picks |
| Second-Guesser | 🔁 | most edited picks (`updated_at − created_at` > 1 s) | ≥ 3 edits |
| The Contrarian | 🃏 | most points from picks whose **result** < 25 % of that match's bettors chose (reuse the Scarcity pool machinery + its ≥ 8-bet quorum, in **every** mode) | > 0 pts |

### Thresholds — everyone who qualifies wears it

| Badge | Emoji | Criterion |
|---|---|---|
| Perfect Round | ✨ | every pick in a round scored points (≥ 3 picks that round) |
| The Blackout | 🕳 | a round with ≥ 3 picks and zero points |
| Hot Hand | 🎯 | 2+ consecutive exact scores |
| Ever-Present | 📅 | bet every available match (`MatchesBet == MatchesAvailable`, ≥ 10 available — availability rule from `computeParticipation`) |
| The Quitter | 🚪 | reuse the defector rule verbatim: `RecentSkips ≥ defectorMinTail && MatchesBet > 0` |
| Wire-to-Wire | 🚂 | ranked #1 in every round since joining (`RoundsAsLeader == len(Trajectory)`, ≥ 5 rounds) |

### Stretch (later, if wanted)

- 🤖 **Robot Slayer** — finished above BETanIA (card-time only; meaningless
  mid-tournament where it's half the pool).
- ⚔ **Rival Crusher** — leads the head-to-head vs their `comment_context`
  rivalry partner.
- 🏔 **Peak & Valley pair**, 🎪 **Chaos pick** (highest-scoring weird scoreline), etc.

### Timing-badge integrity rule

A bet whose `created_at >= match.StartsAt` has **no meaningful timing**: it was
inserted by `ai-seed` or the `place-bet` escape hatch (e.g. bets placed for
players after a match registered with a wrong kickoff). Such picks are
**excluded from Deadline Junkie / Early Bird / Second-Guesser counting** — never
counted as "late", never as "early". BETanIA is additionally ineligible for all
three (her seed backfill makes timing nonsense); identified by the reserved
`bethoven:ai-betania` fingerprint, passed as `IsAI`.

## Architecture

```
internal/achievements   PURE: badge catalog + Compute(players []PlayerInput) Board
internal/service        service_achievements.go: builds PlayerInput (one pass), read API,
                        folds Badges into PlayerCard, feeds ai digests
internal/tui            trophies.go: 🏆 Trophy Room screen; badge row on the player card
internal/ai             CardDigestData.Badges []string (grounding, phase 4)
```

### `internal/achievements` (pure)

```go
type Kind int // Superlative | Threshold

type Badge struct {
    ID    string // stable slug, e.g. "oracle"
    Name  string
    Emoji string
    Desc  string // one-liner shown in the Trophy Room
    Kind  Kind
}

// One player's flattened history. The service builds it; Compute never reads the DB.
type PlayerInput struct {
    UserID   int64
    Name     string
    IsAI     bool // reserved-fingerprint check done by the service
    Picks    []Pick        // finished matches only, chronological (ListMatches order)
    Rounds   []RoundDelta  // this player's trajectory rows from StandingsHistory
    Participation Part     // Available/Bet/Skipped/RecentSkips — from computeParticipation
}

type Pick struct {
    Round          string    // UTC kickoff date, the round key
    PredA, PredB   int
    ScoreA, ScoreB int
    Points         int       // active-mode scorer output
    Exact, Correct, Draw bool
    PlacedAt, UpdatedAt, Kickoff time.Time
    ResultShare    float64   // fraction of the match's bettors on the same result; -1 below quorum
}

type Award struct {
    Badge  Badge
    Detail string // human line: "4 exact scores", "round of Jun 20: +7 pts"
    Value  int    // the number that won it (for ranking/ties)
}

type Board struct {
    Standings []BadgeStanding      // catalog order: badge + current holder(s), empty = unclaimed
    ByUser    map[int64][]Award
}

func Compute(players []PlayerInput) Board
```

Every criterion is a small private `func(...) *Award` evaluated per player;
superlatives then keep the max (ties → multiple holders). Unclaimed badges stay
in `Standings` with no holders — "unclaimed" renders in the UI and is motivating.

### Service (`service_achievements.go`)

- `func (s *Service) Achievements() (achievements.Board, error)` — **ungated**
  read (see Principles). Builder does ONE pass: `AllUsers` + `ListMatches` +
  `AllBets` + `StandingsHistory` + `newScorer` (all existing reads — same shape
  as `buildBetsGrid`/`StandingsHistory`), assembles `PlayerInput` per user,
  calls `Compute`. `ResultShare` reuses the same per-match pick-distribution
  fold `service_scoring.go` builds for Scarcity (extract the helper so both use
  it), applying the quorum in every mode.
- `buildPlayerCards` calls the same builder and attaches `Badges []achievements.Award`
  to `PlayerCard` — cards and Trophy Room can never disagree.
- No analytics changes needed: the Trophy Room gets a `view` event for free from
  the TUI menu-transition tracking.

### TUI

- **🏆 Trophy Room** — new menu entry, `screenTrophies` (`internal/tui/trophies.go`).
  A viewport list in catalog order: `🔮 The Oracle — Edy (4 exact scores)`;
  threshold badges list all wearers; unclaimed superlatives render dimmed as
  `— unclaimed —`. Keys: `↑↓/jk` scroll, `esc` back. Reuse `viewport.go`.
  Refresh on entry only (no tick — the data only moves when a result lands).
- **Player card** (`viewPlayerCard`): a wrapped badge row (emoji + name) under
  the stats block. Card save-to-PNG picks it up automatically.

### BETanIA (phase 4, optional)

- `cardDigest` adds `Badges []string` (the award names + details) so the
  hero's-journey narrative can cite them.
- The per-player comment prompt gets a compact "current badge holders" block via
  `CommentConfig` — one more grounding tier, same never-invent framing. Roast
  material: "three-time Deadline Junkie, and it shows."

## Semantics & edge cases

- **Ties share.** Two players with 4 exacts are both The Oracle. `Detail` shows
  the shared value.
- **Minimums, not zero-noise.** Superlatives below their minimum go unclaimed
  (early tournament) rather than crowning a 1-bet wonder.
- **Mode switches re-derive.** Changing `scoring_mode` recomputes point-based
  badges, same as the leaderboard. Result-shape badges (Oracle, streaks, draws)
  are mode-agnostic via `scoring.IsExact`/`IsCorrectResult`.
- **Muted players still earn badges** — mute governs BETanIA commenting *about*
  them, not their play. (BETanIA just won't narrate their badges.)
- **BETanIA competes** for non-timing badges — she's a player on the board.
- **Late joiners**: rounds before registration are excluded exactly as in
  `buildPlayerCards` (regDate filter), so no artificial Free Fall/Comeback.

## Tests

- `achievements/achievements_test.go` — table-driven per badge: earn / miss /
  minimum-gate / tie / AI-timing-exclusion / post-kickoff-timestamp exclusion.
  Pure structs in, no DB, no clock.
- `service/service_achievements_test.go` — integration on the `newTestService`
  harness: fake clock places bets at controlled offsets before kickoff (the
  Deadline Junkie / Early Bird proof), enter results, assert the Board; assert
  card badges match the Trophy Room; assert a `place-bet`-style post-kickoff
  upsert doesn't count for timing badges.
- `tui` — one render test for the Trophy Room list (golden, like
  `betania_render_test.go`).

## Phasing (each lands independently)

1. **Pure package** — catalog + `Compute` + full test table.
2. **Service** — builder, `Achievements()`, `PlayerCard.Badges`, shared
   pick-distribution helper, integration tests.
3. **TUI** — Trophy Room screen + card badge row.
4. **BETanIA grounding** — digests + comment-context tier (optional, cheap).

Stretch badges and any per-badge "history" (who held it when — would need
storage) are explicitly out of scope for v1.
