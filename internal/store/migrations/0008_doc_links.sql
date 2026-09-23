-- Links between documents (Obsidian-style [[wikilinks]] and relative
-- Markdown links), as written and as resolved against the index. A row
-- with an empty to_path is a broken link. links_scanned holds the link-extraction version a document
-- was parsed with (store.LinksVersion); older ones are re-parsed.
CREATE TABLE doc_links (
    from_path TEXT NOT NULL,
    target    TEXT NOT NULL,
    kind      TEXT NOT NULL,
    to_path   TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (from_path, kind, target)
);
CREATE INDEX doc_links_to ON doc_links(to_path);
ALTER TABLE documents ADD COLUMN links_scanned INTEGER NOT NULL DEFAULT 0;
