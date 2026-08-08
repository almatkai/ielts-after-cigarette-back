DROP INDEX IF EXISTS users_google_sub_unique;
ALTER TABLE users DROP COLUMN IF EXISTS google_sub;
