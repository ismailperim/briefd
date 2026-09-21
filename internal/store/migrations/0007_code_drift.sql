-- Knowledge that lags the code it governs. For each document with `refs`
-- globs, the commits in the configured code repositories that touched a
-- matching path after the document itself last changed (ADR-0007).
CREATE TABLE code_drift (
    doc_path       TEXT PRIMARY KEY,
    repo           TEXT NOT NULL,
    commits        INTEGER NOT NULL,
    last_change_at TEXT NOT NULL,
    last_path      TEXT NOT NULL,
    checked_at     TEXT NOT NULL
);
