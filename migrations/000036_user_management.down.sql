-- Nullable attribution is retained: an erased user cannot be reconstructed.
DO $$ BEGIN IF EXISTS (SELECT 1 FROM account_object_deletions) THEN RAISE EXCEPTION 'Complete pending object deletions before rollback'; END IF; END $$;
DROP TABLE account_object_deletions;
ALTER TABLE users DROP COLUMN blocked, DROP COLUMN token_version;
