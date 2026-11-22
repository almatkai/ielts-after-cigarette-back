-- Fix user deletion blockers introduced after 000036 blanket FK conversion.

-- guest_trials.claimed_by was added without ON DELETE. Clearing must also clear
-- claimed_session_id to satisfy guest_trial_claim_complete.
UPDATE guest_trials SET claimed_by = NULL, claimed_session_id = NULL
WHERE claimed_by IS NOT NULL
  AND claimed_by NOT IN (SELECT id FROM users);

-- Allow deleting reviewer accounts while preserving review history.
ALTER TABLE study_learning_content_metadata
    ALTER COLUMN reviewer_id DROP NOT NULL;
ALTER TABLE study_learning_content_metadata
    DROP CONSTRAINT IF EXISTS study_learning_content_metadata_reviewer_id_fkey;
ALTER TABLE study_learning_content_metadata
    ADD CONSTRAINT study_learning_content_metadata_reviewer_id_fkey
    FOREIGN KEY (reviewer_id) REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE study_learning_content_reviews
    ALTER COLUMN reviewer_id DROP NOT NULL;
ALTER TABLE study_learning_content_reviews
    DROP CONSTRAINT IF EXISTS study_learning_content_reviews_reviewer_id_fkey;
ALTER TABLE study_learning_content_reviews
    ADD CONSTRAINT study_learning_content_reviews_reviewer_id_fkey
    FOREIGN KEY (reviewer_id) REFERENCES users(id) ON DELETE SET NULL;
