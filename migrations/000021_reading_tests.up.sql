ALTER TABLE reading_materials
    ADD COLUMN material_kind VARCHAR(16) NOT NULL DEFAULT 'PASSAGE',
    ADD CONSTRAINT reading_materials_kind_valid
        CHECK (material_kind IN ('PASSAGE', 'TEST'));

ALTER TABLE reading_material_versions
    ADD COLUMN duration_minutes SMALLINT,
    ADD CONSTRAINT reading_material_versions_duration_valid
        CHECK (duration_minutes IS NULL OR duration_minutes BETWEEN 1 AND 300);

CREATE TABLE reading_test_passages (
    test_material_version_id UUID NOT NULL
        REFERENCES reading_material_versions(id) ON DELETE CASCADE,
    position SMALLINT NOT NULL,
    passage_material_id UUID NOT NULL REFERENCES reading_materials(id),
    passage_material_version_id UUID NOT NULL,
    PRIMARY KEY (test_material_version_id, position),
    UNIQUE (test_material_version_id, passage_material_id),
    CONSTRAINT reading_test_passages_position_valid CHECK (position BETWEEN 1 AND 20),
    CONSTRAINT reading_test_passages_version_fk
        FOREIGN KEY (passage_material_id, passage_material_version_id)
        REFERENCES reading_material_versions(material_id, id),
    CONSTRAINT reading_test_passages_distinct_materials
        CHECK (test_material_version_id <> passage_material_version_id)
);

CREATE INDEX reading_test_passages_passage_idx
    ON reading_test_passages (passage_material_id, passage_material_version_id);
