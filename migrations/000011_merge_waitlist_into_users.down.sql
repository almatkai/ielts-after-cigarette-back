-- Restore waitlist_entries and move the still-unregistered leads back into
-- it. Registered accounts keep their enriched columns dropped.

CREATE TABLE waitlist_entries (
    id UUID PRIMARY KEY,
    phone VARCHAR(16) NOT NULL,
    email VARCHAR(254),
    first_name VARCHAR(100),
    last_name VARCHAR(100),
    source VARCHAR(50),
    status VARCHAR(32) NOT NULL DEFAULT 'WAITING',
    phone_verified_at TIMESTAMPTZ,
    google_sub VARCHAR(255),
    referred_by_code VARCHAR(64),
    referral_code VARCHAR(16) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT waitlist_entries_phone_e164 CHECK (phone ~ '^\+[1-9][0-9]{7,14}$'),
    CONSTRAINT waitlist_entries_email_normalized CHECK (
        email IS NULL OR email = LOWER(BTRIM(email))
    ),
    CONSTRAINT waitlist_entries_first_name_not_blank CHECK (
        first_name IS NULL OR BTRIM(first_name) <> ''
    ),
    CONSTRAINT waitlist_entries_last_name_not_blank CHECK (
        last_name IS NULL OR BTRIM(last_name) <> ''
    ),
    CONSTRAINT waitlist_entries_status_valid CHECK (status IN ('WAITING', 'INVITED', 'REGISTERED'))
);

CREATE UNIQUE INDEX waitlist_entries_phone_unique ON waitlist_entries (phone);

CREATE UNIQUE INDEX waitlist_entries_google_sub_unique
    ON waitlist_entries (google_sub)
    WHERE google_sub IS NOT NULL;

CREATE UNIQUE INDEX waitlist_entries_referral_code_unique
    ON waitlist_entries (referral_code);

CREATE INDEX waitlist_entries_referred_by_code_idx
    ON waitlist_entries (referred_by_code)
    WHERE referred_by_code IS NOT NULL;

CREATE TRIGGER waitlist_entries_set_updated_at
    BEFORE UPDATE ON waitlist_entries
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

INSERT INTO waitlist_entries (
    id, phone, email, first_name, last_name, source, status, google_sub,
    referred_by_code, referral_code, created_at, updated_at
)
SELECT
    id,
    phone,
    CASE WHEN email LIKE 'waitlist-%@placeholder.invalid' THEN NULL ELSE email END,
    first_name,
    last_name,
    source,
    status,
    google_sub,
    referred_by_code,
    referral_code,
    created_at,
    updated_at
FROM users
WHERE status IN ('WAITING', 'INVITED');

DELETE FROM users WHERE status IN ('WAITING', 'INVITED');

-- Google-provisioned accounts (super admins) legitimately have no password;
-- the old schema demands one, so backfill before restoring NOT NULL.
UPDATE users SET password_hash = '' WHERE password_hash IS NULL;
UPDATE users SET terms_accepted_at = created_at WHERE terms_accepted_at IS NULL;

DROP INDEX IF EXISTS users_referral_code_unique;
DROP INDEX IF EXISTS users_referred_by_code_idx;

ALTER TABLE users
    DROP COLUMN status,
    DROP COLUMN first_name,
    DROP COLUMN last_name,
    DROP COLUMN source,
    DROP COLUMN referred_by_code,
    DROP COLUMN referral_code,
    ALTER COLUMN password_hash SET NOT NULL,
    ALTER COLUMN terms_accepted_at SET NOT NULL;
