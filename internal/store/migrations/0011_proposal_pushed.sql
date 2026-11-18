-- Whether the proposal branch reached the remote. A branch that was never
-- pushed lives only in the local clone, so its absence on the remote must
-- not be read as "declined". Proposals stored before this column existed
-- are assumed pushed, the normal case.
ALTER TABLE proposals ADD COLUMN pushed INTEGER NOT NULL DEFAULT 1;
