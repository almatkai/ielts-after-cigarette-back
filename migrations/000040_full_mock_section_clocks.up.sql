ALTER TABLE full_mock_session_sections
    ADD COLUMN started_at timestamptz,
    ADD COLUMN deadline_at timestamptz,
    ADD CONSTRAINT full_mock_section_clock CHECK (
        (started_at IS NULL AND deadline_at IS NULL)
        OR (started_at IS NOT NULL AND deadline_at IS NOT NULL AND deadline_at > started_at)
    );
