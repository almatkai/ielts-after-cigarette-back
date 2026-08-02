DROP TRIGGER IF EXISTS waitlist_entries_set_updated_at ON waitlist_entries;
DROP TRIGGER IF EXISTS phone_verifications_set_updated_at ON phone_verifications;
DROP TABLE IF EXISTS waitlist_entries;
DROP TABLE IF EXISTS phone_verifications;
DROP INDEX IF EXISTS users_phone_unique;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_phone_e164;
ALTER TABLE users DROP COLUMN IF EXISTS phone;

