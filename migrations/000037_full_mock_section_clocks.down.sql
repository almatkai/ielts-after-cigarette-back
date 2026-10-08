ALTER TABLE full_mock_session_sections
    DROP CONSTRAINT full_mock_section_clock,
    DROP COLUMN deadline_at,
    DROP COLUMN started_at;
