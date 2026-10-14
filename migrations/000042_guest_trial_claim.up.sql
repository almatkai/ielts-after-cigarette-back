ALTER TABLE guest_trials
    ADD COLUMN claimed_by UUID REFERENCES users(id),
    ADD COLUMN claimed_session_id UUID REFERENCES full_mock_sessions(id),
    ADD CONSTRAINT guest_trial_claim_complete CHECK ((claimed_by IS NULL) = (claimed_session_id IS NULL));
