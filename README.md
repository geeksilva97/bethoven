# 🎼 BEThoven

A no-money, just-for-fun World Cup prediction pool **you play over SSH**.
Connect with `ssh`, pick scores in a terminal UI, and climb the leaderboard.
Inspired by [terminal.shop](https://www.terminal.shop/).

Your **SSH public key is your identity** — no passwords, no signup forms, and
nobody can impersonate a teammate.

## How it works

- Each player connects over SSH and is recognised by their key.
- Before a match kicks off, you predict the **score** and an **over/under 2.5
  goals** bonus.
- Once a match starts, betting is **locked** — the server clock is the only
  authority, so you can't bet on an ongoing or finished game.
- Points per match (max 4):
  - **3** exact score · **1** correct result (W/D/L) · **+1** correct over/under
- Standings come in three flavours: **per-game ranking**, **my results**, and the
  tournament **leaderboard**.

## How to play

You join with an SSH key. You can use an existing one, but most people make a
**dedicated key just for BEThoven** — it keeps your game identity separate and
is easy to share-as-identity with the organiser.

**1. Create a key for the game** (one time):

```sh
ssh-keygen -t ed25519 -f ~/.ssh/bethoven -C "bethoven" -N ""
```

That writes a private key `~/.ssh/bethoven` and a public key `~/.ssh/bethoven.pub`.

**2. Add a connection alias** so you can just type `ssh bethoven`. Append to
`~/.ssh/config`:

```
Host bethoven
    HostName <server-ip-or-hostname>
    Port 2222
    IdentityFile ~/.ssh/bethoven
    IdentitiesOnly yes
```

(For a local test, set `HostName localhost`.)

**3. Connect and register:**

```sh
ssh bethoven
```

On first connect you'll be asked for the **invite code** (the organiser shares
it) and a **display name**. After that, your key *is* you — no code or password
again.

**4. Play:**

- **Place / edit bets** — pick an upcoming match, type the score (e.g. `2` and
  `1`), toggle **over/under 2.5** goals with the space bar, and save. You can
  edit any time until kickoff; after kickoff the match is locked.
- **My results** — your picks, the actual scores, points per match, and total.
- **Leaderboard** — everyone's running totals.
- **Per-game ranking** — who nailed a specific match (picks reveal once the game
  has a result).

Keys: `↑/↓` move · `enter` select · `tab` switch field · `space` toggle
over/under · `b`/`esc` back · `q` quit.

## Run it locally

```sh
make run        # builds and serves on :2222 with invite code "letmein"
```

Then connect from another terminal (`ssh bethoven` if you set up the alias, or
`ssh -p 2222 localhost`). First connect asks for the invite code + display name.

## Admin

Admins add knockout matches and enter results. To become one, add your key
fingerprint to `BETHOVEN_ADMINS`:

```sh
ssh-keygen -lf ~/.ssh/id_ed25519.pub      # -> SHA256:xxxx...
BETHOVEN_ADMINS=SHA256:xxxx... make run
```

A key in that list is auto-promoted to admin on connect (order doesn't matter —
even if you registered as a player first) and skips the invite code. Removing a
fingerprint from the list **revokes** admin on the next connect — the env
allowlist is the source of truth.

## Configuration (env vars)

| Var | Default | Purpose |
|-----|---------|---------|
| `BETHOVEN_PORT` | `2222` | SSH listen port |
| `BETHOVEN_DB_PATH` | `bethoven.db` | SQLite file |
| `BETHOVEN_HOST_KEY_PATH` | `host_key` | persistent SSH host key |
| `BETHOVEN_INVITE_CODE` | `letmein` | shared first-connect secret |
| `BETHOVEN_ADMINS` | — | comma-separated admin fingerprints |

## Fixtures

`fixtures.json` seeds the group stage on first boot (idempotent — it won't
re-import into a populated tournament). Replace it with the official 2026 World
Cup schedule. Knockout matches are added by an admin in the TUI as the bracket
fills in.

## Build

```sh
make build            # host binary
make build-linux      # static linux/amd64 (no cgo — pure-Go SQLite)
make build-linux-arm  # static linux/arm64
make test             # full suite, race detector
```

Because BEThoven uses a pure-Go SQLite driver, the Linux builds are **fully
static, zero-dependency binaries** — cross-compiled from any OS, dropped onto
the server, and run as-is.

## Deploy (e.g. GCE `e2-micro`, but any Linux VM works)

No domain and **no TLS certificate** are needed — SSH provides its own
encryption, and the persistent host key is the server's identity.

**1. Provision a small VM** and reserve a **static IP**. Open an inbound
firewall rule for **TCP 2222** (leave the box's own sshd on 22). Attach a
persistent disk if your provider's root disk isn't durable.

**2. Build and copy the binary + fixtures:**

```sh
make build-linux                                  # -> bethoven-linux-amd64
ssh you@<vm> 'sudo mkdir -p /opt/bethoven/data && sudo useradd -r -s /usr/sbin/nologin bethoven'
scp bethoven-linux-amd64 you@<vm>:/tmp/bethoven
scp fixtures.json        you@<vm>:/tmp/fixtures.json
ssh you@<vm> 'sudo mv /tmp/bethoven /opt/bethoven/bethoven && sudo mv /tmp/fixtures.json /opt/bethoven/ && sudo chown -R bethoven:bethoven /opt/bethoven'
```

**3. Install the service.** Copy `deploy/bethoven.service` to
`/etc/systemd/system/`, then edit its `Environment=` lines — set a real
`BETHOVEN_INVITE_CODE` and your `BETHOVEN_ADMINS` fingerprint
(`ssh-keygen -lf ~/.ssh/bethoven.pub`). Then:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now bethoven
sudo systemctl status bethoven         # confirm it's listening
```

**4. Share with the team:** the static IP, the invite code, and the server's
host-key fingerprint (so they can verify it on first connect):

```sh
ssh you@<vm> 'ssh-keygen -lf /opt/bethoven/data/host_key'
```

Teammates then follow [How to play](#how-to-play): make a key, add the
`~/.ssh/config` alias pointing `HostName` at your static IP, and `ssh bethoven`.

**Updating:** rebuild, `scp` the new binary over `/opt/bethoven/bethoven`, and
`sudo systemctl restart bethoven`. The DB and host key live in
`/opt/bethoven/data`, so identities, bets, and the host fingerprint all survive
restarts and upgrades.

## Architecture

```
cmd/bethoven        entrypoint: config -> db -> seed -> service -> server
internal/config     env configuration
internal/db         SQLite, schema, typed store, seeding
internal/models     pure domain types
internal/clock      injectable time source (real + fake for tests)
internal/auth       key fingerprints, admin allowlist, invite check
internal/scoring    pure point rules (heavily unit-tested)
internal/service    ALL business rules; the integration-test seam
internal/server     thin wish SSH server -> Bubble Tea TUI
internal/tui        terminal UI (presentation only)
```

The server and TUI are thin; every rule lives in `service`, which depends only
on the store and a clock. That seam is what makes the kickoff lock and scoring
testable against a real DB with deterministic time.

## License

MIT — see [LICENSE](LICENSE).
