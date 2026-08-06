DROP INDEX IF EXISTS waitlist_entries_google_sub_unique;
ALTER TABLE waitlist_entries DROP COLUMN IF EXISTS google_sub;
