-- BEThoven schema. Applied with CREATE TABLE IF NOT EXISTS on every boot, so
-- it is safe to re-run. Times are stored as RFC3339 UTC text.

CREATE TABLE IF NOT EXISTS tournaments (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL,
    active     INTEGER NOT NULL DEFAULT 0,
    created_at TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    fingerprint  TEXT    NOT NULL UNIQUE,
    display_name TEXT    NOT NULL,
    role         TEXT    NOT NULL DEFAULT 'player',
    created_at   TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS matches (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    tournament_id INTEGER NOT NULL REFERENCES tournaments(id),
    team_a        TEXT    NOT NULL,
    team_b        TEXT    NOT NULL,
    phase         TEXT    NOT NULL DEFAULT 'group',
    group_label   TEXT    NOT NULL DEFAULT '',
    starts_at     TEXT    NOT NULL,
    score_a       INTEGER,
    score_b       INTEGER,
    finished      INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS bets (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users(id),
    match_id   INTEGER NOT NULL REFERENCES matches(id),
    pred_a     INTEGER NOT NULL,
    pred_b     INTEGER NOT NULL,
    created_at TEXT    NOT NULL,
    updated_at TEXT    NOT NULL,
    UNIQUE(user_id, match_id)
);

-- Runtime-mutable key/value settings, toggled live by admins (e.g. public_bets).
-- Distinct from BETHOVEN_* env config, which is immutable and read once at boot.
CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- BETanIA's per-player leaderboard comments, persisted so they survive restarts.
-- The in-memory ai.CommentCache is the hot read path; this is its durable backing
-- store, so a deploy doesn't regenerate every comment from scratch. One row per
-- user holds their latest comment.
CREATE TABLE IF NOT EXISTS leaderboard_comments (
    user_id    INTEGER PRIMARY KEY REFERENCES users(id),
    player     TEXT    NOT NULL,
    text       TEXT    NOT NULL,
    created_at TEXT    NOT NULL,
    expires_at TEXT    NOT NULL DEFAULT ''
);

-- BETanIA's end-of-tournament "player cards": one persisted hero's-journey
-- narrative per player, generated on demand by an admin. Only the AI narrative is
-- stored; the trajectory and stats on the card are recomputed at read time. One
-- row per user holds their latest narrative. Mirrors leaderboard_comments.
CREATE TABLE IF NOT EXISTS player_cards (
    user_id      INTEGER PRIMARY KEY REFERENCES users(id),
    narrative    TEXT    NOT NULL,
    generated_at TEXT    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_matches_tournament ON matches(tournament_id);
CREATE INDEX IF NOT EXISTS idx_bets_match ON bets(match_id);
CREATE INDEX IF NOT EXISTS idx_bets_user ON bets(user_id);
