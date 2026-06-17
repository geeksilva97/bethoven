-- Analytics event log. This database is SEPARATE from the domain database
-- (bethoven.db) and is never joined against it at the SQL level — the actor's
-- display name is denormalized into each row so the admin panel is
-- self-contained. Nothing here affects bets or scoring; losing this file loses
-- only usage history.

CREATE TABLE IF NOT EXISTS events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    at          TEXT    NOT NULL,            -- RFC3339 UTC (sorts lexically)
    user_id     INTEGER NOT NULL DEFAULT 0,  -- 0 when unregistered/anonymous
    fingerprint TEXT    NOT NULL DEFAULT '',
    actor       TEXT    NOT NULL DEFAULT '', -- display name at event time (or "(unregistered)")
    name        TEXT    NOT NULL,            -- event type, e.g. "session_start", "bet_placed"
    props       TEXT    NOT NULL DEFAULT '{}' -- JSON object of event-specific fields
);

CREATE INDEX IF NOT EXISTS idx_events_at   ON events(at);
CREATE INDEX IF NOT EXISTS idx_events_user ON events(user_id);
CREATE INDEX IF NOT EXISTS idx_events_name ON events(name);
