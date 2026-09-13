ALTER TABLE writing_material_versions
  ADD COLUMN duration_minutes INTEGER NOT NULL DEFAULT 60
  CHECK (duration_minutes BETWEEN 5 AND 180);
