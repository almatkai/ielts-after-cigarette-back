ALTER TABLE attempts DROP CONSTRAINT attempts_status_valid;
ALTER TABLE attempts ADD CONSTRAINT attempts_status_valid
    CHECK (status IN ('IN_PROGRESS', 'PROCESSING', 'SUBMITTED', 'ABANDONED'));

ALTER TABLE attempts DROP CONSTRAINT attempts_submitted_fields;
ALTER TABLE attempts ADD CONSTRAINT attempts_submitted_fields
    CHECK (
        status IN ('IN_PROGRESS', 'PROCESSING', 'ABANDONED') OR
        (score IS NOT NULL AND max_score IS NOT NULL AND submitted_at IS NOT NULL)
    );

ALTER TABLE speaking_recordings
    ADD COLUMN revision INTEGER NOT NULL DEFAULT 1,
    ADD CONSTRAINT speaking_recordings_revision_valid CHECK (revision > 0);

CREATE TABLE speaking_transcriptions (
    recording_id UUID PRIMARY KEY REFERENCES speaking_recordings(id) ON DELETE CASCADE,
    recording_revision INTEGER NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'QUEUED',
    provider VARCHAR(64) NOT NULL DEFAULT 'faster-whisper',
    model VARCHAR(128) NOT NULL DEFAULT '',
    language VARCHAR(16) NOT NULL DEFAULT 'en',
    language_probability DOUBLE PRECISION,
    transcript TEXT NOT NULL DEFAULT '',
    audio_duration_ms BIGINT,
    speech_duration_ms BIGINT,
    processing_time_ms BIGINT,
    words JSONB NOT NULL DEFAULT '[]'::jsonb,
    segments JSONB NOT NULL DEFAULT '[]'::jsonb,
    metrics JSONB NOT NULL DEFAULT '{}'::jsonb,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    error_code VARCHAR(64),
    error_message TEXT,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT speaking_transcriptions_status_valid
        CHECK (status IN ('QUEUED', 'PROCESSING', 'READY', 'FAILED')),
    CONSTRAINT speaking_transcriptions_revision_valid CHECK (recording_revision > 0),
    CONSTRAINT speaking_transcriptions_attempts_valid CHECK (attempts BETWEEN 0 AND 3),
    CONSTRAINT speaking_transcriptions_words_array CHECK (jsonb_typeof(words) = 'array'),
    CONSTRAINT speaking_transcriptions_segments_array CHECK (jsonb_typeof(segments) = 'array'),
    CONSTRAINT speaking_transcriptions_metrics_object CHECK (jsonb_typeof(metrics) = 'object')
);

CREATE INDEX speaking_transcriptions_work_idx
    ON speaking_transcriptions (status, next_attempt_at, created_at);

INSERT INTO speaking_transcriptions (recording_id, recording_revision)
SELECT r.id, r.revision
FROM speaking_recordings r
JOIN attempts a ON a.id = r.attempt_id
WHERE a.status IN ('IN_PROGRESS', 'PROCESSING')
ON CONFLICT (recording_id) DO NOTHING;

CREATE TABLE speaking_assessment_jobs (
    attempt_id UUID PRIMARY KEY REFERENCES attempts(id) ON DELETE CASCADE,
    status VARCHAR(16) NOT NULL DEFAULT 'QUEUED',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    error_code VARCHAR(64),
    error_message TEXT,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT speaking_assessment_jobs_status_valid
        CHECK (status IN ('QUEUED', 'PROCESSING', 'READY', 'FAILED')),
    CONSTRAINT speaking_assessment_jobs_attempts_valid CHECK (attempts BETWEEN 0 AND 3)
);

CREATE INDEX speaking_assessment_jobs_work_idx
    ON speaking_assessment_jobs (status, next_attempt_at, created_at);

ALTER TABLE speaking_evaluations
    ALTER COLUMN pronunciation_band DROP NOT NULL,
    DROP CONSTRAINT speaking_evaluations_band_valid;

ALTER TABLE speaking_evaluations ADD CONSTRAINT speaking_evaluations_band_valid CHECK (
    overall_band BETWEEN 0 AND 9 AND
    fluency_band BETWEEN 0 AND 9 AND
    lexical_resource_band BETWEEN 0 AND 9 AND
    grammar_band BETWEEN 0 AND 9 AND
    (pronunciation_band IS NULL OR pronunciation_band BETWEEN 0 AND 9)
);
