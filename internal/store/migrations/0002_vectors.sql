-- Chunk embeddings (SPEC §6/§7, ADR-0002). Not a foreign key on purpose: chunks
-- are replaced wholesale when a document changes, and vectors for chunks whose
-- id and content_hash are unchanged must survive that so they are not
-- re-embedded. Orphans are pruned after every index run.
CREATE TABLE chunk_vectors (
    chunk_id     TEXT PRIMARY KEY,
    model        TEXT    NOT NULL,
    dim          INTEGER NOT NULL,
    content_hash TEXT    NOT NULL,
    embedding    BLOB    NOT NULL   -- little-endian float32[dim], L2-normalised
);
