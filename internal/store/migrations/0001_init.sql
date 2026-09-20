-- Documents and chunks (SPEC §6). Everything here is a disposable cache of
-- the knowledge git repository and can be rebuilt from a fresh clone.

CREATE TABLE documents (
    id             INTEGER PRIMARY KEY,
    path           TEXT    NOT NULL UNIQUE,
    scope          TEXT    NOT NULL,
    title          TEXT    NOT NULL,
    tags           TEXT    NOT NULL DEFAULT '[]', -- JSON array of strings
    refs           TEXT    NOT NULL DEFAULT '[]', -- JSON array of code path globs
    front_matter   TEXT    NOT NULL DEFAULT '',
    content_hash   TEXT    NOT NULL,
    updated_commit TEXT    NOT NULL DEFAULT '',
    indexed_at     TEXT    NOT NULL
);
CREATE INDEX documents_scope ON documents(scope);

-- rowid is declared explicitly because the FTS index is keyed on it and must
-- survive VACUUM. `title` is denormalized from documents so the FTS external
-- content table can index it without a join.
CREATE TABLE chunks (
    rowid        INTEGER PRIMARY KEY,
    id           TEXT    NOT NULL UNIQUE,
    doc_id       INTEGER NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    title        TEXT    NOT NULL,
    heading_path TEXT    NOT NULL,
    content      TEXT    NOT NULL,
    tokens       INTEGER NOT NULL,
    content_hash TEXT    NOT NULL,
    position     INTEGER NOT NULL
);
CREATE INDEX chunks_doc ON chunks(doc_id, position);

-- External-content FTS5 index over chunks. Porter stemming helps English
-- paraphrases; remove_diacritics=2 folds accented characters so mixed-language
-- corpora (e.g. Turkish terms) match with or without diacritics.
CREATE VIRTUAL TABLE chunks_fts USING fts5(
    title,
    heading_path,
    content,
    content='chunks',
    content_rowid='rowid',
    tokenize='porter unicode61 remove_diacritics 2'
);

CREATE TRIGGER chunks_ai AFTER INSERT ON chunks BEGIN
    INSERT INTO chunks_fts(rowid, title, heading_path, content)
    VALUES (new.rowid, new.title, new.heading_path, new.content);
END;
CREATE TRIGGER chunks_ad AFTER DELETE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, title, heading_path, content)
    VALUES ('delete', old.rowid, old.title, old.heading_path, old.content);
END;
CREATE TRIGGER chunks_au AFTER UPDATE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, title, heading_path, content)
    VALUES ('delete', old.rowid, old.title, old.heading_path, old.content);
    INSERT INTO chunks_fts(rowid, title, heading_path, content)
    VALUES (new.rowid, new.title, new.heading_path, new.content);
END;

CREATE TABLE sync_state (
    id           INTEGER PRIMARY KEY CHECK (id = 1),
    source       TEXT NOT NULL DEFAULT '', -- repo URL or local path
    last_commit  TEXT NOT NULL DEFAULT '',
    last_sync_at TEXT,
    last_error   TEXT NOT NULL DEFAULT ''
);
INSERT INTO sync_state(id) VALUES (1);
