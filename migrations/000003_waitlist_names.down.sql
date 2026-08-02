ALTER TABLE waitlist_entries
    DROP CONSTRAINT waitlist_entries_first_name_not_blank,
    DROP CONSTRAINT waitlist_entries_last_name_not_blank,
    DROP COLUMN last_name;

ALTER TABLE waitlist_entries
    RENAME COLUMN first_name TO display_name;

ALTER TABLE waitlist_entries
    ADD CONSTRAINT waitlist_entries_display_name_not_blank CHECK (
        display_name IS NULL OR BTRIM(display_name) <> ''
    );
