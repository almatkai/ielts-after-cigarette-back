ALTER TABLE users DROP CONSTRAINT users_role_valid;
ALTER TABLE users ADD CONSTRAINT users_role_valid CHECK (role IN ('STUDENT', 'EDITOR', 'ADMIN', 'GUEST'));
ALTER TABLE users DROP CONSTRAINT users_status_valid;
ALTER TABLE users ADD CONSTRAINT users_status_valid CHECK (status IN ('WAITING', 'INVITED', 'REGISTERED', 'GUEST'));
ALTER TABLE users ADD CONSTRAINT users_guest_status_valid CHECK ((role = 'GUEST') = (status = 'GUEST'));
ALTER TABLE users DROP CONSTRAINT users_referral_code_required_for_leads;
ALTER TABLE users ADD CONSTRAINT users_referral_code_required_for_leads CHECK (status IN ('REGISTERED', 'GUEST') OR referral_code IS NOT NULL);

-- Guest actors reuse attempt ownership, but never receive account credentials.
CREATE TABLE guest_trials (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX guest_trials_expiry_idx ON guest_trials (expires_at);
