ALTER TABLE attempts DROP CONSTRAINT attempts_material_type_valid;
ALTER TABLE attempts ADD CONSTRAINT attempts_material_type_valid
    CHECK (material_type IN ('listening', 'reading', 'writing', 'speaking'));

CREATE TABLE speaking_materials (
    id UUID PRIMARY KEY,
    slug VARCHAR(160) NOT NULL UNIQUE,
    status VARCHAR(16) NOT NULL DEFAULT 'DRAFT',
    revision BIGINT NOT NULL DEFAULT 1,
    current_version_number INTEGER NOT NULL DEFAULT 1,
    published_version_id UUID,
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT speaking_materials_status_valid CHECK (status IN ('DRAFT', 'PUBLISHED', 'ARCHIVED')),
    CONSTRAINT speaking_materials_revision_valid CHECK (revision > 0),
    CONSTRAINT speaking_materials_version_valid CHECK (current_version_number > 0)
);

CREATE TABLE speaking_material_versions (
    id UUID PRIMARY KEY,
    material_id UUID NOT NULL REFERENCES speaking_materials(id) ON DELETE CASCADE,
    version_number INTEGER NOT NULL,
    exam_type VARCHAR(16) NOT NULL,
    difficulty VARCHAR(16) NOT NULL,
    title VARCHAR(200) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    parts JSONB NOT NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT speaking_material_versions_unique UNIQUE (material_id, version_number),
    CONSTRAINT speaking_material_versions_exam_type_valid CHECK (exam_type IN ('academic', 'general')),
    CONSTRAINT speaking_material_versions_difficulty_valid CHECK (difficulty IN ('foundation', 'intermediate', 'advanced')),
    CONSTRAINT speaking_material_versions_parts_array CHECK (jsonb_typeof(parts) = 'array')
);

ALTER TABLE speaking_materials
    ADD CONSTRAINT speaking_materials_published_version_fk
    FOREIGN KEY (published_version_id) REFERENCES speaking_material_versions(id) ON DELETE SET NULL;

CREATE INDEX speaking_materials_published_idx ON speaking_materials (status, published_at DESC);
CREATE INDEX speaking_material_versions_material_idx ON speaking_material_versions (material_id, version_number DESC);

CREATE TABLE speaking_recordings (
    id UUID PRIMARY KEY,
    attempt_id UUID NOT NULL REFERENCES attempts(id) ON DELETE CASCADE,
    part_id UUID NOT NULL,
    original_name VARCHAR(255) NOT NULL,
    mime_type VARCHAR(100) NOT NULL,
    storage_key VARCHAR(255) NOT NULL UNIQUE,
    byte_size BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT speaking_recordings_size_valid CHECK (byte_size > 0),
    CONSTRAINT speaking_recordings_part_unique UNIQUE (attempt_id, part_id)
);

CREATE INDEX speaking_recordings_attempt_idx ON speaking_recordings (attempt_id, created_at);

CREATE TABLE speaking_evaluations (
    attempt_id UUID PRIMARY KEY REFERENCES attempts(id) ON DELETE CASCADE,
    model VARCHAR(200) NOT NULL,
    overall_band NUMERIC(2,1) NOT NULL,
    fluency_band NUMERIC(2,1) NOT NULL,
    lexical_resource_band NUMERIC(2,1) NOT NULL,
    grammar_band NUMERIC(2,1) NOT NULL,
    pronunciation_band NUMERIC(2,1) NOT NULL,
    feedback JSONB NOT NULL,
    evaluated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT speaking_evaluations_band_valid CHECK (
        overall_band BETWEEN 0 AND 9 AND
        fluency_band BETWEEN 0 AND 9 AND
        lexical_resource_band BETWEEN 0 AND 9 AND
        grammar_band BETWEEN 0 AND 9 AND
        pronunciation_band BETWEEN 0 AND 9
    ),
    CONSTRAINT speaking_evaluations_feedback_object CHECK (jsonb_typeof(feedback) = 'object')
);
