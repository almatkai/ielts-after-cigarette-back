INSERT INTO speaking_transcriptions (recording_id, recording_revision)
SELECT r.id, r.revision
FROM speaking_recordings r
JOIN attempts a ON a.id = r.attempt_id
WHERE a.status IN ('IN_PROGRESS', 'PROCESSING')
ON CONFLICT (recording_id) DO NOTHING;
