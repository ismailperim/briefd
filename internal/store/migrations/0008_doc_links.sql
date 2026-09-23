-- Links between documents (Obsidian-style [[wikilinks]] and relative
-- Markdown links), as written and as resolved against the index. A row
-- with an empty to_path is a broken link. links_scanned marks documents
-- indexed since links were introduced, so an upgrade re-parses the rest.
CREATE TABLE doc_links (
    from_path TEXT NOT NULL,
    target    TEXT NOT NULL,
    kind      TEXT NOT NULL,
    to_path   TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (from_path, kind, target)
);
CREATE INDEX doc_links_to ON doc_links(to_path);
ALTER TABLE documents ADD COLUMN links_scanned INTEGER NOT NULL DEFAULT 0;
