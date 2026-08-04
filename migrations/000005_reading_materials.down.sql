DROP TRIGGER IF EXISTS reading_materials_set_updated_at ON reading_materials;
ALTER TABLE reading_materials
    DROP CONSTRAINT IF EXISTS reading_materials_current_version_fk,
    DROP CONSTRAINT IF EXISTS reading_materials_published_version_fk;
DROP TABLE IF EXISTS reading_material_versions;
DROP TABLE IF EXISTS reading_materials;
