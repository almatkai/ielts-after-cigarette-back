-- Merge waitlist_entries into users: a waitlist lead becomes a users row
-- with status WAITING that registration later completes in place.
-- password_hash and terms_accepted_at become nullable because leads have
-- neither.

ALTER TABLE users
    ALTER COLUMN password_hash DROP NOT NULL,
    ALTER COLUMN terms_accepted_at DROP NOT NULL,
    ADD COLUMN status VARCHAR(32) NOT NULL DEFAULT 'REGISTERED',
    ADD COLUMN first_name VARCHAR(100),
    ADD COLUMN last_name VARCHAR(100),
    ADD COLUMN source VARCHAR(50),
    ADD COLUMN referred_by_code VARCHAR(64),
    ADD COLUMN referral_code VARCHAR(16),
    ADD CONSTRAINT users_status_valid CHECK (status IN ('WAITING', 'INVITED', 'REGISTERED')),
    ADD CONSTRAINT users_first_name_not_blank CHECK (
        first_name IS NULL OR BTRIM(first_name) <> ''
    ),
    ADD CONSTRAINT users_last_name_not_blank CHECK (
        last_name IS NULL OR BTRIM(last_name) <> ''
    ),
    ADD CONSTRAINT users_referral_code_required_for_leads CHECK (
        status = 'REGISTERED' OR referral_code IS NOT NULL
    );

CREATE UNIQUE INDEX users_referral_code_unique
    ON users (referral_code)
    WHERE referral_code IS NOT NULL;

CREATE INDEX users_referred_by_code_idx
    ON users (referred_by_code)
    WHERE referred_by_code IS NOT NULL;

-- Carry every waitlist entry over, oldest first. An entry that obviously
-- belongs to an existing account (same Google identity, email, or phone)
-- enriches that row instead of duplicating it; the rest become fresh
-- WAITING users. users.email is NOT NULL, so an entry without an email gets
-- a unique placeholder that the API renders back as NULL.
DO $$
DECLARE
    entry RECORD;
    target UUID;
BEGIN
    FOR entry IN SELECT * FROM waitlist_entries ORDER BY created_at ASC LOOP
        target := NULL;

        IF entry.google_sub IS NOT NULL THEN
            SELECT id INTO target FROM users WHERE google_sub = entry.google_sub;
        END IF;
        IF target IS NULL AND entry.email IS NOT NULL THEN
            SELECT id INTO target FROM users WHERE LOWER(email) = LOWER(entry.email);
        END IF;
        IF target IS NULL THEN
            SELECT id INTO target FROM users WHERE phone = entry.phone;
        END IF;

        IF target IS NOT NULL THEN
            UPDATE users SET
                first_name = COALESCE(users.first_name, entry.first_name),
                last_name = COALESCE(users.last_name, entry.last_name),
                source = COALESCE(users.source, entry.source),
                referred_by_code = COALESCE(users.referred_by_code, entry.referred_by_code),
                google_sub = CASE
                    WHEN users.google_sub IS NULL AND entry.google_sub IS NOT NULL
                        AND NOT EXISTS (
                            SELECT 1 FROM users u2
                            WHERE u2.google_sub = entry.google_sub AND u2.id <> target
                        )
                    THEN entry.google_sub
                    ELSE users.google_sub
                END,
                phone = CASE
                    WHEN users.phone IS NULL
                        AND NOT EXISTS (
                            SELECT 1 FROM users u2
                            WHERE u2.phone = entry.phone AND u2.id <> target
                        )
                    THEN entry.phone
                    ELSE users.phone
                END,
                created_at = LEAST(users.created_at, entry.created_at)
            WHERE id = target;
        ELSE
            BEGIN
                INSERT INTO users (
                    id, email, phone, role, status, first_name, last_name, source,
                    google_sub, referral_code, referred_by_code, created_at, updated_at
                ) VALUES (
                    entry.id,
                    COALESCE(entry.email, 'waitlist-' || entry.id::text || '@placeholder.invalid'),
                    entry.phone,
                    'STUDENT',
                    entry.status,
                    entry.first_name,
                    entry.last_name,
                    entry.source,
                    entry.google_sub,
                    entry.referral_code,
                    entry.referred_by_code,
                    entry.created_at,
                    entry.updated_at
                );
            EXCEPTION WHEN unique_violation THEN
                -- Defensive: duplicates normally merge via the email/phone/
                -- google_sub match above (waitlist_entries.email is not
                -- unique, so two leads can share one). If an unexpected
                -- collision still slips through, keep the lead's data under
                -- a placeholder email; it can still be completed through its
                -- google_sub.
                INSERT INTO users (
                    id, email, phone, role, status, first_name, last_name, source,
                    google_sub, referral_code, referred_by_code, created_at, updated_at
                ) VALUES (
                    entry.id,
                    'waitlist-' || entry.id::text || '@placeholder.invalid',
                    entry.phone,
                    'STUDENT',
                    entry.status,
                    entry.first_name,
                    entry.last_name,
                    entry.source,
                    entry.google_sub,
                    entry.referral_code,
                    entry.referred_by_code,
                    entry.created_at,
                    entry.updated_at
                );
            END;
        END IF;
    END LOOP;
END $$;

DROP TABLE waitlist_entries;
