CREATE TABLE reading_materials (
    id UUID PRIMARY KEY,
    slug VARCHAR(160) NOT NULL UNIQUE,
    exam_type VARCHAR(16) NOT NULL,
    difficulty VARCHAR(24) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'DRAFT',
    current_version_id UUID,
    published_version_id UUID,
    revision BIGINT NOT NULL DEFAULT 1,
    created_by UUID NOT NULL REFERENCES users(id),
    updated_by UUID NOT NULL REFERENCES users(id),
    published_by UUID REFERENCES users(id),
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT reading_materials_slug_valid CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'),
    CONSTRAINT reading_materials_exam_type_valid CHECK (exam_type IN ('academic', 'general')),
    CONSTRAINT reading_materials_difficulty_valid CHECK (difficulty IN ('foundation', 'intermediate', 'advanced')),
    CONSTRAINT reading_materials_status_valid CHECK (status IN ('DRAFT', 'PUBLISHED', 'ARCHIVED')),
    CONSTRAINT reading_materials_revision_valid CHECK (revision > 0),
    CONSTRAINT reading_materials_published_state_valid CHECK (
        (published_version_id IS NULL AND published_at IS NULL AND published_by IS NULL AND status = 'DRAFT')
        OR
        (published_version_id IS NOT NULL AND published_at IS NOT NULL AND published_by IS NOT NULL AND status IN ('PUBLISHED', 'ARCHIVED'))
    )
);

CREATE TABLE reading_material_versions (
    id UUID PRIMARY KEY,
    material_id UUID NOT NULL REFERENCES reading_materials(id) ON DELETE CASCADE,
    version_number INTEGER NOT NULL,
    title VARCHAR(200) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL,
    source_title VARCHAR(200),
    source_url TEXT,
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT reading_material_versions_number_valid CHECK (version_number > 0),
    CONSTRAINT reading_material_versions_title_not_blank CHECK (BTRIM(title) <> ''),
    CONSTRAINT reading_material_versions_body_not_blank CHECK (BTRIM(body) <> ''),
    CONSTRAINT reading_material_versions_material_number_unique UNIQUE (material_id, version_number),
    CONSTRAINT reading_material_versions_material_id_unique UNIQUE (material_id, id)
);

ALTER TABLE reading_materials
    ADD CONSTRAINT reading_materials_current_version_fk
    FOREIGN KEY (id, current_version_id)
    REFERENCES reading_material_versions(material_id, id),
    ADD CONSTRAINT reading_materials_published_version_fk
    FOREIGN KEY (id, published_version_id)
    REFERENCES reading_material_versions(material_id, id);

CREATE INDEX reading_materials_catalog_idx
    ON reading_materials (status, exam_type, difficulty, updated_at DESC);

CREATE TRIGGER reading_materials_set_updated_at
    BEFORE UPDATE ON reading_materials
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
