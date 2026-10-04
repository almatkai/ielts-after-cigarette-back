-- Generated exams store their metadata on the session, not in the mock catalog.
ALTER TABLE full_mock_sessions
    ALTER COLUMN mock_test_id DROP NOT NULL,
    ADD COLUMN exam_type VARCHAR(16),
    ADD COLUMN title VARCHAR(200),
    ADD COLUMN duration_minutes INTEGER,
    ADD CONSTRAINT full_mock_sessions_exam_type_valid CHECK (exam_type IN ('academic', 'general')),
    ADD CONSTRAINT full_mock_sessions_duration_valid CHECK (duration_minutes BETWEEN 60 AND 300);

UPDATE full_mock_sessions s
SET exam_type=t.exam_type, title=t.title, duration_minutes=t.duration_minutes
FROM full_mock_tests t WHERE t.id=s.mock_test_id;

ALTER TABLE full_mock_sessions ADD CONSTRAINT full_mock_sessions_metadata_valid
    CHECK (mock_test_id IS NOT NULL OR (exam_type IS NOT NULL AND title IS NOT NULL AND duration_minutes IS NOT NULL));

-- Legacy exams may have several active sessions per user. The generator resumes
-- these first; this index protects new generated sessions without deleting history.
CREATE UNIQUE INDEX full_mock_sessions_generated_active_user_idx
    ON full_mock_sessions (user_id) WHERE status='IN_PROGRESS' AND mock_test_id IS NULL;
CREATE INDEX attempts_completed_material_idx
    ON attempts (user_id, material_type, material_id) WHERE status IN ('SUBMITTED', 'PROCESSING');
