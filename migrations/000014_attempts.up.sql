CREATE TABLE attempts (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    material_type VARCHAR(16) NOT NULL,
    material_id UUID NOT NULL,
    material_version_id UUID NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'IN_PROGRESS',
    score SMALLINT,
    max_score SMALLINT,
    band NUMERIC(2,1),
    started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    submitted_at TIMESTAMPTZ,
    CONSTRAINT attempts_material_type_valid CHECK (material_type IN ('listening')),
    CONSTRAINT attempts_status_valid CHECK (status IN ('IN_PROGRESS', 'SUBMITTED')),
    CONSTRAINT attempts_submitted_fields CHECK (
        status = 'IN_PROGRESS' OR
        (score IS NOT NULL AND max_score IS NOT NULL AND submitted_at IS NOT NULL)
    ),
    CONSTRAINT attempts_score_valid CHECK (
        score IS NULL OR (max_score IS NOT NULL AND score BETWEEN 0 AND max_score)
    ),
    CONSTRAINT attempts_band_valid CHECK (
        band IS NULL OR (band BETWEEN 0 AND 9 AND band * 2 = TRUNC(band * 2))
    )
);

CREATE INDEX attempts_user_material_idx ON attempts (user_id, material_type, started_at DESC);

CREATE TABLE attempt_answers (
    attempt_id UUID NOT NULL REFERENCES attempts(id) ON DELETE CASCADE,
    question_id UUID NOT NULL,
    answer JSONB NOT NULL DEFAULT '{}'::jsonb,
    is_correct BOOLEAN,
    points_awarded SMALLINT,
    CONSTRAINT attempt_answers_pk PRIMARY KEY (attempt_id, question_id),
    CONSTRAINT attempt_answers_answer_object CHECK (jsonb_typeof(answer) = 'object'),
    CONSTRAINT attempt_answers_points_valid CHECK (
        points_awarded IS NULL OR points_awarded >= 0
    )
);
