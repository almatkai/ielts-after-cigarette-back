ALTER TABLE users
    ADD COLUMN phone VARCHAR(16),
    ADD CONSTRAINT users_phone_e164 CHECK (
        phone IS NULL OR phone ~ '^\+[1-9][0-9]{7,14}$'
    );

CREATE UNIQUE INDEX users_phone_unique
    ON users (phone)
    WHERE phone IS NOT NULL;

CREATE TABLE phone_verifications (
    id UUID PRIMARY KEY,
    phone VARCHAR(16) NOT NULL,
    purpose VARCHAR(32) NOT NULL,
    code_hash BYTEA NOT NULL,
    attempts SMALLINT NOT NULL DEFAULT 0,
    max_attempts SMALLINT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    verified_at TIMESTAMPTZ,
    verification_token_hash BYTEA,
    token_expires_at TIMESTAMPTZ,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT phone_verifications_phone_e164 CHECK (phone ~ '^\+[1-9][0-9]{7,14}$'),
    CONSTRAINT phone_verifications_purpose_valid CHECK (
        purpose IN ('WAITLIST', 'REGISTRATION')
    ),
    CONSTRAINT phone_verifications_attempts_valid CHECK (
        attempts >= 0 AND max_attempts > 0 AND attempts <= max_attempts
    ),
    CONSTRAINT phone_verifications_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT phone_verifications_verified_state_valid CHECK (
        (verified_at IS NULL AND verification_token_hash IS NULL AND token_expires_at IS NULL) OR
        (verified_at IS NOT NULL AND verification_token_hash IS NOT NULL AND token_expires_at > verified_at)
    ),
    CONSTRAINT phone_verifications_consumed_state_valid CHECK (
        consumed_at IS NULL OR verified_at IS NOT NULL
    )
);

CREATE UNIQUE INDEX phone_verifications_token_unique
    ON phone_verifications (verification_token_hash)
    WHERE verification_token_hash IS NOT NULL;

CREATE INDEX phone_verifications_phone_purpose_created_idx
    ON phone_verifications (phone, purpose, created_at DESC);

CREATE TABLE waitlist_entries (
    id UUID PRIMARY KEY,
    phone VARCHAR(16) NOT NULL,
    email VARCHAR(254),
    display_name VARCHAR(100),
    source VARCHAR(50),
    status VARCHAR(32) NOT NULL DEFAULT 'WAITING',
    phone_verified_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT waitlist_entries_phone_e164 CHECK (phone ~ '^\+[1-9][0-9]{7,14}$'),
    CONSTRAINT waitlist_entries_email_normalized CHECK (
        email IS NULL OR email = LOWER(BTRIM(email))
    ),
    CONSTRAINT waitlist_entries_display_name_not_blank CHECK (
        display_name IS NULL OR BTRIM(display_name) <> ''
    ),
    CONSTRAINT waitlist_entries_status_valid CHECK (status IN ('WAITING', 'INVITED', 'REGISTERED'))
);

CREATE UNIQUE INDEX waitlist_entries_phone_unique ON waitlist_entries (phone);

CREATE TRIGGER phone_verifications_set_updated_at
    BEFORE UPDATE ON phone_verifications
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER waitlist_entries_set_updated_at
    BEFORE UPDATE ON waitlist_entries
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

