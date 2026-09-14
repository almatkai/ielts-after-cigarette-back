DROP TABLE IF EXISTS reading_test_passages;

ALTER TABLE reading_material_versions
    DROP CONSTRAINT IF EXISTS reading_material_versions_duration_valid,
    DROP COLUMN IF EXISTS duration_minutes;

ALTER TABLE reading_materials
    DROP CONSTRAINT IF EXISTS reading_materials_kind_valid,
    DROP COLUMN IF EXISTS material_kind;
