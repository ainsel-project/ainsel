-- Rolling back forgets any pending forced change: the affected accounts keep
-- whatever password they hold, which is exactly the pre-#113 behaviour. No
-- data depends on the column, so nothing has to be reconstructed.
ALTER TABLE users DROP COLUMN must_change_password;
