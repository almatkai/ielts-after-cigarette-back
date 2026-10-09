ALTER TABLE users ADD COLUMN blocked BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN token_version INTEGER NOT NULL DEFAULT 0;

-- Retain shared teaching materials while removing the deleted account's attribution.
DO $$
DECLARE ref RECORD;
BEGIN
    FOR ref IN SELECT c.conname, c.conrelid::regclass AS tbl, a.attname AS col
        FROM pg_constraint c
        JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
        WHERE c.contype = 'f' AND c.confrelid = 'users'::regclass
          AND c.confdeltype = 'a'
    LOOP
        EXECUTE format('ALTER TABLE %s ALTER COLUMN %I DROP NOT NULL', ref.tbl, ref.col);
        EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I', ref.tbl, ref.conname);
        EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I FOREIGN KEY (%I) REFERENCES users(id) ON DELETE SET NULL', ref.tbl, ref.conname, ref.col);
    END LOOP;
END $$;

ALTER TABLE reading_materials DROP CONSTRAINT reading_materials_published_state_valid;
ALTER TABLE reading_materials ADD CONSTRAINT reading_materials_published_state_valid CHECK (
 (published_version_id IS NULL AND published_at IS NULL AND published_by IS NULL AND status='DRAFT') OR
 (published_version_id IS NOT NULL AND published_at IS NOT NULL AND status IN ('PUBLISHED','ARCHIVED'))
);

-- Durable cleanup survives process restarts and temporary storage outages.
CREATE TABLE account_object_deletions (
    store VARCHAR(16) NOT NULL CHECK (store IN ('blog', 'speaking')),
    storage_key TEXT NOT NULL,
    PRIMARY KEY (store, storage_key)
);
