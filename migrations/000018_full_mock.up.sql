CREATE TABLE full_mock_tests (
    id UUID PRIMARY KEY,
    slug VARCHAR(160) NOT NULL UNIQUE,
    status VARCHAR(16) NOT NULL DEFAULT 'DRAFT',
    revision BIGINT NOT NULL DEFAULT 1,
    exam_type VARCHAR(16) NOT NULL,
    title VARCHAR(200) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    duration_minutes INTEGER NOT NULL DEFAULT 165,
    listening_material_id UUID NOT NULL,
    reading_material_id UUID NOT NULL,
    writing_material_id UUID NOT NULL,
    speaking_material_id UUID NOT NULL,
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT full_mock_tests_status_valid CHECK (status IN ('DRAFT', 'PUBLISHED', 'ARCHIVED')),
    CONSTRAINT full_mock_tests_exam_type_valid CHECK (exam_type IN ('academic', 'general')),
    CONSTRAINT full_mock_tests_duration_valid CHECK (duration_minutes BETWEEN 60 AND 300),
    CONSTRAINT full_mock_tests_revision_valid CHECK (revision > 0)
);

CREATE INDEX full_mock_tests_published_idx ON full_mock_tests (status, published_at DESC);

CREATE TABLE full_mock_sessions (
    id UUID PRIMARY KEY,
    mock_test_id UUID NOT NULL REFERENCES full_mock_tests(id) ON DELETE RESTRICT,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status VARCHAR(16) NOT NULL DEFAULT 'IN_PROGRESS',
    current_section SMALLINT NOT NULL DEFAULT 1,
    started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    submitted_at TIMESTAMPTZ,
    CONSTRAINT full_mock_sessions_status_valid CHECK (status IN ('IN_PROGRESS', 'SUBMITTED')),
    CONSTRAINT full_mock_sessions_section_valid CHECK (current_section BETWEEN 1 AND 5)
);

CREATE UNIQUE INDEX full_mock_sessions_active_user_idx
    ON full_mock_sessions (mock_test_id, user_id) WHERE status = 'IN_PROGRESS';
CREATE INDEX full_mock_sessions_user_idx ON full_mock_sessions (user_id, started_at DESC);

CREATE TABLE full_mock_session_sections (
    session_id UUID NOT NULL REFERENCES full_mock_sessions(id) ON DELETE CASCADE,
    position SMALLINT NOT NULL,
    skill VARCHAR(16) NOT NULL,
    attempt_id UUID NOT NULL UNIQUE REFERENCES attempts(id) ON DELETE RESTRICT,
    CONSTRAINT full_mock_session_sections_pk PRIMARY KEY (session_id, position),
    CONSTRAINT full_mock_session_sections_position_valid CHECK (position BETWEEN 1 AND 4),
    CONSTRAINT full_mock_session_sections_skill_valid CHECK (skill IN ('listening', 'reading', 'writing', 'speaking')),
    CONSTRAINT full_mock_session_sections_position_skill_valid CHECK (
        (position = 1 AND skill = 'listening') OR
        (position = 2 AND skill = 'reading') OR
        (position = 3 AND skill = 'writing') OR
        (position = 4 AND skill = 'speaking')
    )
);
