ALTER TABLE speaking_evaluations DROP CONSTRAINT speaking_evaluations_band_valid;
UPDATE speaking_evaluations SET pronunciation_band = 0 WHERE pronunciation_band IS NULL;
ALTER TABLE speaking_evaluations ALTER COLUMN pronunciation_band SET NOT NULL;
ALTER TABLE speaking_evaluations ADD CONSTRAINT speaking_evaluations_band_valid CHECK (
    overall_band BETWEEN 0 AND 9 AND fluency_band BETWEEN 0 AND 9 AND
    lexical_resource_band BETWEEN 0 AND 9 AND grammar_band BETWEEN 0 AND 9 AND
    pronunciation_band BETWEEN 0 AND 9
);

DROP TABLE IF EXISTS speaking_assessment_jobs;
DROP TABLE IF EXISTS speaking_transcriptions;
ALTER TABLE speaking_recordings DROP CONSTRAINT speaking_recordings_revision_valid;
ALTER TABLE speaking_recordings DROP COLUMN revision;

UPDATE attempts SET status='IN_PROGRESS' WHERE status='PROCESSING';

ALTER TABLE attempts DROP CONSTRAINT attempts_submitted_fields;
ALTER TABLE attempts ADD CONSTRAINT attempts_submitted_fields CHECK (
    status IN ('IN_PROGRESS', 'ABANDONED') OR
    (score IS NOT NULL AND max_score IS NOT NULL AND submitted_at IS NOT NULL)
);
ALTER TABLE attempts DROP CONSTRAINT attempts_status_valid;
ALTER TABLE attempts ADD CONSTRAINT attempts_status_valid
    CHECK (status IN ('IN_PROGRESS', 'SUBMITTED', 'ABANDONED'));
