ALTER TABLE listening_parts ADD COLUMN IF NOT EXISTS transcript TEXT NOT NULL DEFAULT '';
ALTER TABLE listening_parts ADD COLUMN IF NOT EXISTS transcript_segments JSONB NOT NULL DEFAULT '[]'::jsonb;
