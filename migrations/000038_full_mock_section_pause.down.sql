-- Resume paused clocks before removing their saved time.
ALTER TABLE full_mock_session_sections
    DROP CONSTRAINT full_mock_section_pause_valid,
    DROP CONSTRAINT full_mock_section_clock;
UPDATE full_mock_session_sections
SET deadline_at = CURRENT_TIMESTAMP + remaining_milliseconds * INTERVAL '1 millisecond'
WHERE remaining_milliseconds IS NOT NULL;
ALTER TABLE full_mock_session_sections
    DROP COLUMN remaining_milliseconds,
    ADD CONSTRAINT full_mock_section_clock CHECK (
        (started_at IS NULL AND deadline_at IS NULL)
        OR (started_at IS NOT NULL AND deadline_at IS NOT NULL AND deadline_at > started_at)
    );
