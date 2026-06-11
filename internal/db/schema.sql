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

CREATE INDEX IF NOT EXISTS idx_matches_tournament ON matches(tournament_id);
CREATE INDEX IF NOT EXISTS idx_bets_match ON bets(match_id);
CREATE INDEX IF NOT EXISTS idx_bets_user ON bets(user_id);
