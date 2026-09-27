CREATE TABLE IF NOT EXISTS writing_assessment_jobs (
    attempt_id UUID PRIMARY KEY REFERENCES attempts(id) ON DELETE CASCADE,
    status VARCHAR(16) NOT NULL DEFAULT 'QUEUED',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    error_code VARCHAR(64),
    error_message TEXT,
    started_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT writing_assessment_jobs_status_valid CHECK (status IN ('QUEUED', 'PROCESSING', 'READY', 'FAILED')),
    CONSTRAINT writing_assessment_jobs_attempts_valid CHECK (attempts BETWEEN 0 AND 3)
);

CREATE INDEX IF NOT EXISTS writing_assessment_jobs_work_idx
    ON writing_assessment_jobs (status, next_attempt_at, created_at);
