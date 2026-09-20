-- Compiled bundles and usage feedback (SPEC §3.1, §5, §6).

-- index_fingerprint identifies the exact knowledge state the index holds:
-- a hash over every (path, content_hash). Unlike a git commit it also
-- changes for plain-directory sources, so bundle cache keys stay correct
-- without git.
ALTER TABLE sync_state ADD COLUMN index_fingerprint TEXT NOT NULL DEFAULT '';

CREATE TABLE bundles (
    id                TEXT PRIMARY KEY,           -- short hash of cache_key, returned to clients
    cache_key         TEXT NOT NULL UNIQUE,
    task              TEXT NOT NULL,
    scopes            TEXT NOT NULL,              -- JSON array, sorted
    max_tokens        INTEGER NOT NULL,
    index_fingerprint TEXT NOT NULL,
    model             TEXT NOT NULL,              -- embedding model or "" for bm25
    content           TEXT NOT NULL,
    tokens            INTEGER NOT NULL,
    sections          TEXT NOT NULL,              -- JSON array of {chunk_id, doc_path, heading, tokens}
    truncated         INTEGER NOT NULL DEFAULT 0,
    created_at        TEXT NOT NULL,
    hits              INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE usage_events (
    id         INTEGER PRIMARY KEY,
    bundle_id  TEXT NOT NULL,
    chunk_id   TEXT NOT NULL,
    useful     INTEGER NOT NULL,
    client     TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX usage_events_bundle ON usage_events(bundle_id);
