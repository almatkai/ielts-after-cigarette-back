ALTER TABLE attempts
    DROP CONSTRAINT attempts_status_valid;

ALTER TABLE attempts
    ADD CONSTRAINT attempts_status_valid
    CHECK (status IN ('IN_PROGRESS', 'SUBMITTED', 'ABANDONED'));

ALTER TABLE attempts
    DROP CONSTRAINT attempts_submitted_fields;

ALTER TABLE attempts
    ADD CONSTRAINT attempts_submitted_fields
    CHECK (
        status IN ('IN_PROGRESS', 'ABANDONED') OR
        (score IS NOT NULL AND max_score IS NOT NULL AND submitted_at IS NOT NULL)
    );
