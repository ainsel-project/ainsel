-- Rolling back forgets which rows were plugin-owned; the rows themselves
-- survive as ordinary skills. Nothing else depends on the column, so no data
-- has to be reconstructed.
ALTER TABLE skills DROP COLUMN managed_by;
