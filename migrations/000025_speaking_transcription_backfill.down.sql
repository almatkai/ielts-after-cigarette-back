-- Data backfill is intentionally retained. Migration 000024 owns and drops
-- the speaking_transcriptions table when the whole feature is rolled back.
SELECT 1;
