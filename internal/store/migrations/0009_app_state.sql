-- Small pieces of process state worth keeping across restarts (dashboard
-- counters). Everything here is derived and safe to delete.
CREATE TABLE app_state (
    key        TEXT PRIMARY KEY,
    value      BLOB NOT NULL,
    updated_at TEXT NOT NULL
);
