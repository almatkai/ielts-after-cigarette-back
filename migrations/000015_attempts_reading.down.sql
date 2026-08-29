ALTER TABLE attempts DROP CONSTRAINT attempts_material_type_valid;
ALTER TABLE attempts ADD CONSTRAINT attempts_material_type_valid
    CHECK (material_type IN ('listening'));
