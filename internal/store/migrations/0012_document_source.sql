-- The complete source file (front matter and body, byte for byte), so
-- get_document can hand agents the text propose_update replaces (ADR-0013).
-- NULL for documents indexed before this migration; the indexer re-parses
-- those once even when their content is unchanged.
ALTER TABLE documents ADD COLUMN source TEXT;
