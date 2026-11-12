-- Data-only migration: the rows 0035 returned to the queue cannot be told
-- apart from ordinary pending rows, so there is nothing to restore.
SELECT 1;
