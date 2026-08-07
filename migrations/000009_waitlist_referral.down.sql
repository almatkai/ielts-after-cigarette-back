DROP INDEX waitlist_entries_referred_by_code_idx;

DROP INDEX waitlist_entries_referral_code_unique;

ALTER TABLE waitlist_entries
    DROP COLUMN referral_code;

ALTER TABLE waitlist_entries
    DROP COLUMN referred_by_code;
