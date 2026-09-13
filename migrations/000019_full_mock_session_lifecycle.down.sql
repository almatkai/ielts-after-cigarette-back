ALTER TABLE full_mock_sessions
  DROP CONSTRAINT full_mock_sessions_status_valid;

ALTER TABLE full_mock_sessions
  ADD CONSTRAINT full_mock_sessions_status_valid
  CHECK (status IN ('IN_PROGRESS', 'SUBMITTED'));
