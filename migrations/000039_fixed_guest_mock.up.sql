-- The public trial owns a permanent set of version-pinned materials.
CREATE TABLE guest_mock_materials (
    skill TEXT PRIMARY KEY CHECK (skill IN ('listening','reading','writing','speaking')),
    material_id UUID NOT NULL,
    material_version_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
