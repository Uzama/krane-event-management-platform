DROP TABLE session_exceptions;
DROP INDEX sessions_series_id_idx;
ALTER TABLE sessions DROP COLUMN series_id;
DROP TABLE session_series;
