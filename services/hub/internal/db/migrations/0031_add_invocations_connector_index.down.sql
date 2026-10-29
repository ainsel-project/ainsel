-- Rolling back only drops the index; the connector filter keeps working, as a scan.
DROP INDEX IF EXISTS invocations_connector_start_idx;
