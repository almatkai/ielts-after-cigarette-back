CREATE TABLE listening_tests (
    id UUID PRIMARY KEY,
    slug VARCHAR(160) NOT NULL UNIQUE,
    exam_type VARCHAR(16) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'DRAFT',
    revision BIGINT NOT NULL DEFAULT 1,
    current_version_id UUID,
    published_version_id UUID,
    published_at TIMESTAMPTZ,
    created_by UUID NOT NULL REFERENCES users(id),
    updated_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT listening_tests_exam_type_valid CHECK (exam_type IN ('academic', 'general')),
    CONSTRAINT listening_tests_status_valid CHECK (status IN ('DRAFT', 'PUBLISHED', 'ARCHIVED'))
);

CREATE TABLE listening_media (
    id UUID PRIMARY KEY,
    kind VARCHAR(16) NOT NULL,
    original_name VARCHAR(255) NOT NULL,
    mime_type VARCHAR(100) NOT NULL,
    storage_key VARCHAR(255) NOT NULL UNIQUE,
    byte_size BIGINT NOT NULL,
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT listening_media_kind_valid CHECK (kind IN ('audio', 'image')),
    CONSTRAINT listening_media_size_valid CHECK (byte_size > 0)
);

CREATE TABLE listening_test_versions (
    id UUID PRIMARY KEY,
    test_id UUID NOT NULL REFERENCES listening_tests(id) ON DELETE CASCADE,
    version_number INTEGER NOT NULL,
    title VARCHAR(200) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    duration_minutes SMALLINT NOT NULL DEFAULT 40,
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT listening_versions_number_valid CHECK (version_number > 0),
    CONSTRAINT listening_versions_title_valid CHECK (BTRIM(title) <> ''),
    CONSTRAINT listening_versions_duration_valid CHECK (duration_minutes BETWEEN 1 AND 180),
    CONSTRAINT listening_versions_test_number_unique UNIQUE (test_id, version_number),
    CONSTRAINT listening_versions_test_id_unique UNIQUE (test_id, id)
);

ALTER TABLE listening_tests ADD CONSTRAINT listening_tests_current_version_fk
    FOREIGN KEY (id, current_version_id) REFERENCES listening_test_versions(test_id, id);
ALTER TABLE listening_tests ADD CONSTRAINT listening_tests_published_version_fk
    FOREIGN KEY (id, published_version_id) REFERENCES listening_test_versions(test_id, id);

CREATE TABLE listening_parts (
    id UUID PRIMARY KEY,
    test_version_id UUID NOT NULL REFERENCES listening_test_versions(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    title VARCHAR(200) NOT NULL DEFAULT '',
    audio_asset_id UUID REFERENCES listening_media(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT listening_parts_position_valid CHECK (position > 0),
    CONSTRAINT listening_parts_version_position_unique UNIQUE (test_version_id, position)
);

CREATE TABLE listening_question_groups (
    id UUID PRIMARY KEY,
    part_id UUID NOT NULL REFERENCES listening_parts(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    question_type VARCHAR(48) NOT NULL,
    instructions TEXT NOT NULL DEFAULT '',
    context TEXT NOT NULL DEFAULT '',
    config JSONB NOT NULL DEFAULT '{}'::jsonb,
    image_asset_id UUID REFERENCES listening_media(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT listening_groups_position_valid CHECK (position > 0),
    CONSTRAINT listening_groups_type_valid CHECK (question_type IN (
        'multiple_choice', 'matching', 'map_labelling', 'plan_labelling',
        'diagram_labelling', 'form_completion', 'note_completion',
        'table_completion', 'flow_chart_completion', 'sentence_completion',
        'short_answer'
    )),
    CONSTRAINT listening_groups_config_object CHECK (jsonb_typeof(config) = 'object'),
    CONSTRAINT listening_groups_part_position_unique UNIQUE (part_id, position)
);

CREATE TABLE listening_questions (
    id UUID PRIMARY KEY,
    group_id UUID NOT NULL REFERENCES listening_question_groups(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    number INTEGER NOT NULL,
    prompt TEXT NOT NULL,
    content JSONB NOT NULL DEFAULT '{}'::jsonb,
    answer JSONB NOT NULL DEFAULT '{}'::jsonb,
    explanation TEXT NOT NULL DEFAULT '',
    points SMALLINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT listening_questions_position_valid CHECK (position > 0),
    CONSTRAINT listening_questions_number_valid CHECK (number > 0),
    CONSTRAINT listening_questions_prompt_valid CHECK (BTRIM(prompt) <> ''),
    CONSTRAINT listening_questions_content_object CHECK (jsonb_typeof(content) = 'object'),
    CONSTRAINT listening_questions_answer_object CHECK (jsonb_typeof(answer) = 'object'),
    CONSTRAINT listening_questions_points_valid CHECK (points BETWEEN 1 AND 10),
    CONSTRAINT listening_questions_group_position_unique UNIQUE (group_id, position)
);

CREATE INDEX listening_tests_status_idx ON listening_tests (status, updated_at DESC);
CREATE INDEX listening_versions_test_idx ON listening_test_versions (test_id, version_number DESC);
CREATE INDEX listening_parts_version_idx ON listening_parts (test_version_id, position);
CREATE INDEX listening_groups_part_idx ON listening_question_groups (part_id, position);
CREATE INDEX listening_questions_group_idx ON listening_questions (group_id, position);
