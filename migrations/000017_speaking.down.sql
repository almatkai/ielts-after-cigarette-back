DROP TABLE IF EXISTS speaking_evaluations;
DROP TABLE IF EXISTS speaking_recordings;
DROP TABLE IF EXISTS speaking_material_versions;
DROP TABLE IF EXISTS speaking_materials;

ALTER TABLE attempts DROP CONSTRAINT attempts_material_type_valid;
ALTER TABLE attempts ADD CONSTRAINT attempts_material_type_valid
    CHECK (material_type IN ('listening', 'reading', 'writing'));
