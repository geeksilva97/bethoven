# CLAUDE.md — BEThoven

World Cup prediction pool played **over SSH**. Players connect with `ssh`, pick
scores in a Bubble Tea TUI, and compete on a leaderboard. A player's **SSH public
key is their identity** — no passwords, no signup forms.

## Stack

- **Go** (module `bethoven`, single static binary).
- **charmbracelet/wish** — SSH server. **charmbracelet/bubbletea** + **bubbles** +
  **lipgloss** — TUI. **charmbracelet/wish/bubbletea** — bridges a session to a Tea program.
- **modernc.org/sqlite** — pure-Go SQLite (no cgo), via `database/sql`.

## Architecture — the layering is the point

```
cmd/bethoven        entrypoint: config -> db.Open -> seed -> service.New -> server.New
internal/config     env config (BETHOVEN_*)
internal/clock      Clock interface: Real (prod) + Fake (tests). INJECTED everywhere time matters.
internal/models     pure domain types (no behaviour, no deps) — shared by store/scoring/service
internal/db         SQLite: Open, embedded schema.sql, Store (typed queries), seed.go
internal/auth       key fingerprint, admin allowlist, invite-code check
internal/scoring    PURE Points(bet, match) — the heavily unit-tested core
internal/service    ALL business rules. Depends only on Store + Clock. The integration-test seam.
internal/server     thin wish SSH server -> resolves key to user -> launches TUI
internal/tui        presentation only; every action calls a service method
```

**Golden rule:** business logic lives in `internal/service`. `server` and `tui`
are thin. If you're tempted to put a rule in the TUI, put it in the service and
call it from the TUI — that's how it stays testable (tests drive the service
directly, with a fake clock, no terminal).

## Core rules / invariants

- **Kickoff lock (most important):** you can only bet while `clock.Now().UTC() <
  match.StartsAt`. Enforced in `service.PlaceBet` using the **server clock only** —
  never trust client time. Also rejects `match.Finished` (belt-and-suspenders for
  clock skew / early result entry). Invariant: *cannot bet an ongoing or ended match.*
- **Own-bets-only:** player queries are scoped to their `user_id`; players never
  see another player's individual picks. Only the admin `AllBets` grid exposes raw
  picks, and it's gated by `requireAdmin` in the service (not just hidden in the UI).
- **Scoring** (`scoring.Points`, max 4/match): exact score = 3; correct result
  (W/D/L) only = 1; +1 if over/under-2.5 bonus is right. `>2` goals == over 2.5.
  Knockouts store the **regulation 90' score**, so ET/penalties are ignored and a
  1-1 a.e.t. scores as a 1-1 draw.
- **Identity = SHA256 key fingerprint.** Set once at registration, immutable after.

## Onboarding & admin

- First connect with an **unknown key** -> registration screen (invite code +
  display name). Wrong code -> rejected, no user created. Admin keys skip the code.
- **Admins** are set via `BETHOVEN_ADMINS` (comma-separated fingerprints from
  `ssh-keygen -lf key.pub`). `service.Resolve` **reconciles the role both ways** on
  connect: an allowlisted key is promoted to admin, and a stored admin no longer in
  the list is **demoted** to player. The env allowlist is the single source of
  truth — add a fingerprint and connect (order doesn't matter); remove it and the
  next connect revokes admin.
- **Display names** are validated server-side in `Register`: rejected (not silently
  stripped) if they contain control chars/ANSI escapes (`ErrBadName`) or duplicate an
  existing name case-insensitively (`ErrNameTaken`). They're rendered into other
  players' terminals, so this is a security boundary, not cosmetics.

## Run / test / build

```sh
make run              # build + serve on :2222, invite code "letmein"
make test             # go test -race ./...  (hermetic: temp DBs, ephemeral ports)
make build-linux      # static linux/amd64 (cross-compiles cleanly — no cgo)
```
Connect: `ssh -p 2222 localhost` (or set up a `~/.ssh/config` alias; see README).

## Tests

- `scoring` — table-driven unit tests (watch the bonus: an "under" prediction that's
  correct adds +1, which is easy to forget when hand-computing expected points).
- `service/*_test.go` — integration vs a **real temp SQLite** + **fake clock**. The
  headline is `TestKickoffLock`. Shared harness `newTestService` lives in `service_test.go`.
- `server/server_test.go` — real wish server on `:0` + real `x/crypto/ssh` client;
  asserts registration vs menu rendering. Uses a sleep-then-quit pattern to capture
  the first frame.
- `tui/tui_test.go` — `teatest` drives the Tea model directly (registration flow).

## GOTCHAS (read before you trip)

- **wish writes a stray host key.** `wish.NewServer` generates its own
  `id_ed25519` in the **current working dir** if no host signer is set *at
  construction time*. We pass the key via `wish.WithHostKeyPEM(...)` as an option
  (see `server.New` / `hostkey.go`). **Do NOT** switch to `AddHostKey` after
  `NewServer` — that reintroduces the leak (it's what put `id_ed25519` in the repo
  root and `internal/server/` originally). `id_ed25519`/`*.pem` are gitignored as a backstop.
- **Store methods called from `service` must be EXPORTED.** Go can't call an
  unexported method across packages. An unexported helper on `*db.Store` used by
  the service won't compile (this bit `BetsForMatch`, originally `queryBetsForMatch`).
- **SQLite is single-writer.** `db.Open` sets `SetMaxOpenConns(1)` + WAL +
  busy_timeout. Don't bump max conns — concurrent writers cause "database is locked".
  This is fine: one tiny server, low traffic.
- **Don't call `time.Now()` in business logic.** Use the injected `Clock`
  (`service.Now()` / `s.clock.Now()`), or the kickoff-lock tests can't control time.
  `clock.Real` is the only place wall-clock is read in the service path.
- **No TLS, by design.** SSH provides encryption + server identity (the host key).
  There is no cert. The **persistent host key** matters: if it changes, every client
  gets a scary "host key changed" warning. It lives at `BETHOVEN_HOST_KEY_PATH`
  (default `host_key`; `/opt/bethoven/data/host_key` in prod) — keep it on durable disk.
- **Run on port 2222, not 22.** Port 22 is the VM's own sshd; clashing locks you out.
- **`activeterm` requires a PTY.** SSH transport tests must `RequestPty` before
  `Shell()`, or the connection is rejected.
- **Bubble Tea Model is passed by value.** `Update` returns `(tea.Model, tea.Cmd)`.
  Mutating helpers take a value receiver and return the modified `Model` (e.g.
  `goMenu`, `openBet`); pointer-receiver helpers (`setStatus`, `focusReg`) mutate in
  place — don't mix them up or edits silently vanish.
- **Times are RFC3339 UTC text in SQLite.** Always `.UTC()` before storing; the store
  formats/parses with `time.RFC3339`.
- **`fixtures.json` is placeholder data.** Replace with the official 2026 schedule
  before launch. Seeding is idempotent — it only imports into an *empty* tournament,
  so editing the file after first boot does nothing; knockouts are added via the admin TUI.

## Deploy (short)

Static binary + `fixtures.json` to `/opt/bethoven`, install
`deploy/bethoven.service` (systemd, unprivileged user, env vars), open TCP 2222,
`systemctl enable --now bethoven`. DB + host key persist under
`/opt/bethoven/data`. No domain/TLS needed. Full steps in README.md.
