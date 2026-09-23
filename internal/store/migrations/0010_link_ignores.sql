-- Link suggestions a maintainer dismissed ("these two documents do not need
-- a link"). Keyed by the pair, so a dismissal survives re-indexing.
CREATE TABLE link_ignores (
    from_path  TEXT NOT NULL,
    to_path    TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (from_path, to_path)
);
