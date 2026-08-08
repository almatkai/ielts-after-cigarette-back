-- Question groups and questions belong to an immutable material version. This
-- prevents a draft edit from changing a test that students already started.
CREATE TABLE reading_question_groups (
    id UUID PRIMARY KEY,
    material_version_id UUID NOT NULL REFERENCES reading_material_versions(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    question_type VARCHAR(48) NOT NULL,
    instructions TEXT NOT NULL DEFAULT '',
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT reading_question_groups_position_valid CHECK (position > 0),
    CONSTRAINT reading_question_groups_type_valid CHECK (question_type IN (
        'multiple_choice', 'true_false_not_given', 'yes_no_not_given',
        'matching_information', 'matching_headings', 'matching_features',
        'matching_sentence_endings', 'sentence_completion', 'summary_completion',
        'note_completion', 'table_completion', 'flow_chart_completion',
        'diagram_label_completion', 'short_answer'
    )),
    CONSTRAINT reading_question_groups_version_position_unique UNIQUE (material_version_id, position)
);

CREATE TABLE reading_questions (
    id UUID PRIMARY KEY,
    group_id UUID NOT NULL REFERENCES reading_question_groups(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    prompt TEXT NOT NULL,
    content JSONB NOT NULL DEFAULT '{}'::jsonb,
    answer JSONB NOT NULL DEFAULT '{}'::jsonb,
    explanation TEXT NOT NULL DEFAULT '',
    points SMALLINT NOT NULL DEFAULT 1,
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT reading_questions_position_valid CHECK (position > 0),
    CONSTRAINT reading_questions_prompt_valid CHECK (BTRIM(prompt) <> ''),
    CONSTRAINT reading_questions_points_valid CHECK (points BETWEEN 1 AND 10),
    CONSTRAINT reading_questions_content_object CHECK (jsonb_typeof(content) = 'object'),
    CONSTRAINT reading_questions_answer_object CHECK (jsonb_typeof(answer) = 'object'),
    CONSTRAINT reading_questions_group_position_unique UNIQUE (group_id, position)
);

CREATE INDEX reading_question_groups_version_idx
    ON reading_question_groups (material_version_id, position);

CREATE INDEX reading_questions_group_idx
    ON reading_questions (group_id, position);
