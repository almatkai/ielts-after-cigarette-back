DROP TABLE IF EXISTS writing_evaluations;
DROP TABLE IF EXISTS writing_material_versions;
DROP TABLE IF EXISTS writing_materials;

ALTER TABLE attempts DROP CONSTRAINT attempts_material_type_valid;
ALTER TABLE attempts ADD CONSTRAINT attempts_material_type_valid
    CHECK (material_type IN ('listening', 'reading'));
