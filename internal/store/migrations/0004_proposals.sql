-- Agent-proposed knowledge changes, materialized as git branches (SPEC §3.1.5).
CREATE TABLE proposals (
    id          TEXT PRIMARY KEY,
    branch      TEXT NOT NULL,
    doc_path    TEXT NOT NULL,
    description TEXT NOT NULL,
    commit_hash TEXT NOT NULL DEFAULT '',
    pr_url      TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'open',   -- open | merged | closed
    client      TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL
);
