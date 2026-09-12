ALTER TABLE attempts DROP CONSTRAINT attempts_material_type_valid;
ALTER TABLE attempts ADD CONSTRAINT attempts_material_type_valid
    CHECK (material_type IN ('listening', 'reading', 'writing'));

CREATE TABLE writing_materials (
    id UUID PRIMARY KEY,
    slug VARCHAR(160) NOT NULL UNIQUE,
    status VARCHAR(16) NOT NULL DEFAULT 'DRAFT',
    revision BIGINT NOT NULL DEFAULT 1,
    current_version_number INTEGER NOT NULL DEFAULT 1,
    published_version_id UUID,
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT writing_materials_status_valid CHECK (status IN ('DRAFT', 'PUBLISHED', 'ARCHIVED')),
    CONSTRAINT writing_materials_revision_valid CHECK (revision > 0),
    CONSTRAINT writing_materials_version_valid CHECK (current_version_number > 0)
);

CREATE TABLE writing_material_versions (
    id UUID PRIMARY KEY,
    material_id UUID NOT NULL REFERENCES writing_materials(id) ON DELETE CASCADE,
    version_number INTEGER NOT NULL,
    exam_type VARCHAR(16) NOT NULL,
    difficulty VARCHAR(16) NOT NULL,
    title VARCHAR(200) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    tasks JSONB NOT NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT writing_material_versions_unique UNIQUE (material_id, version_number),
    CONSTRAINT writing_material_versions_exam_type_valid CHECK (exam_type IN ('academic', 'general')),
    CONSTRAINT writing_material_versions_difficulty_valid CHECK (difficulty IN ('foundation', 'intermediate', 'advanced')),
    CONSTRAINT writing_material_versions_tasks_array CHECK (jsonb_typeof(tasks) = 'array')
);

ALTER TABLE writing_materials
    ADD CONSTRAINT writing_materials_published_version_fk
    FOREIGN KEY (published_version_id) REFERENCES writing_material_versions(id) ON DELETE SET NULL;

CREATE INDEX writing_materials_published_idx ON writing_materials (status, published_at DESC);
CREATE INDEX writing_material_versions_material_idx ON writing_material_versions (material_id, version_number DESC);

CREATE TABLE writing_evaluations (
    attempt_id UUID PRIMARY KEY REFERENCES attempts(id) ON DELETE CASCADE,
    model VARCHAR(200) NOT NULL,
    overall_band NUMERIC(2,1) NOT NULL,
    task_response_band NUMERIC(2,1) NOT NULL,
    coherence_band NUMERIC(2,1) NOT NULL,
    lexical_resource_band NUMERIC(2,1) NOT NULL,
    grammar_band NUMERIC(2,1) NOT NULL,
    feedback JSONB NOT NULL,
    evaluated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT writing_evaluations_band_valid CHECK (
        overall_band BETWEEN 0 AND 9 AND
        task_response_band BETWEEN 0 AND 9 AND
        coherence_band BETWEEN 0 AND 9 AND
        lexical_resource_band BETWEEN 0 AND 9 AND
        grammar_band BETWEEN 0 AND 9
    ),
    CONSTRAINT writing_evaluations_feedback_object CHECK (jsonb_typeof(feedback) = 'object')
);
