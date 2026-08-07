ALTER TABLE waitlist_entries
    ADD COLUMN referred_by_code VARCHAR(64);

ALTER TABLE waitlist_entries
    ADD COLUMN referral_code VARCHAR(16);

UPDATE waitlist_entries
    SET referral_code = LOWER(SUBSTR(MD5(id::text), 1, 8))
    WHERE referral_code IS NULL;

ALTER TABLE waitlist_entries
    ALTER COLUMN referral_code SET NOT NULL;

CREATE UNIQUE INDEX waitlist_entries_referral_code_unique
    ON waitlist_entries (referral_code);

CREATE INDEX waitlist_entries_referred_by_code_idx
    ON waitlist_entries (referred_by_code)
    WHERE referred_by_code IS NOT NULL;
