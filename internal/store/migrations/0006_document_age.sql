-- When a document's content last changed: the committer time of the last
-- commit that touched it for git sources, the file's mtime otherwise.
-- Shown in bundle attribution lines and on the dashboard so readers can
-- tell fresh knowledge from stale.
ALTER TABLE documents ADD COLUMN updated_at TEXT NOT NULL DEFAULT '';
