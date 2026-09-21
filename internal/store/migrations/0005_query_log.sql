-- Query log: what agents asked and how confident retrieval was, kept for
-- a configurable number of days. Feeds the knowledge-gap report (questions
-- that produced no useful section) and future usage-driven ranking.
CREATE TABLE query_log (
    id         INTEGER PRIMARY KEY,
    at         TEXT    NOT NULL,
    surface    TEXT    NOT NULL,             -- mcp | rest
    name       TEXT    NOT NULL,             -- search_context | compile_bundle | api_search | api_bundle
    query      TEXT    NOT NULL,
    scopes     TEXT    NOT NULL DEFAULT '',  -- comma-separated
    mode       TEXT    NOT NULL DEFAULT '',
    results    INTEGER NOT NULL DEFAULT 0,   -- sections returned
    top_score  REAL    NOT NULL DEFAULT 0,   -- best vector cosine (0 in bm25 mode)
    margin     REAL    NOT NULL DEFAULT 0,   -- top_score minus the median of the top-10 cosines
    tokens     INTEGER NOT NULL DEFAULT 0,
    bundle_id  TEXT    NOT NULL DEFAULT '',
    client     TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX query_log_at ON query_log(at);
CREATE INDEX query_log_bundle ON query_log(bundle_id);

-- Retrieval confidence recorded with each bundle so cache hits can be logged
-- with the same numbers as the compile that produced them.
ALTER TABLE bundles ADD COLUMN top_score REAL NOT NULL DEFAULT 0;
ALTER TABLE bundles ADD COLUMN margin    REAL NOT NULL DEFAULT 0;
