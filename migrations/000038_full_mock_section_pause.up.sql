ALTER TABLE full_mock_session_sections DROP CONSTRAINT full_mock_section_clock;
ALTER TABLE full_mock_session_sections
    ADD COLUMN remaining_milliseconds BIGINT,
    ADD CONSTRAINT full_mock_section_pause_valid CHECK (
        remaining_milliseconds IS NULL OR
        (remaining_milliseconds > 0 AND started_at IS NOT NULL AND deadline_at IS NULL)
    ),
    ADD CONSTRAINT full_mock_section_clock CHECK (
        (started_at IS NULL AND deadline_at IS NULL AND remaining_milliseconds IS NULL)
        OR (started_at IS NOT NULL AND (
            (deadline_at IS NOT NULL AND deadline_at > started_at AND remaining_milliseconds IS NULL)
            OR (deadline_at IS NULL AND remaining_milliseconds IS NOT NULL)
        ))
    );
