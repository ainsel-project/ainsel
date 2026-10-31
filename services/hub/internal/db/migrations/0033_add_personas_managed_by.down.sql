-- Rolling back forgets which personas were externally managed; the rows
-- themselves survive as ordinary console-owned personas. Nothing else depends
-- on the column, so no data has to be reconstructed.
ALTER TABLE personas DROP COLUMN managed_by;
