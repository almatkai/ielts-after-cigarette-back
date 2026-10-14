-- Ownership transfers are intentional and remain with the account on rollback.
ALTER TABLE guest_trials
    DROP CONSTRAINT guest_trial_claim_complete,
    DROP COLUMN claimed_session_id,
    DROP COLUMN claimed_by;
