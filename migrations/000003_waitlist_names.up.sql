ALTER TABLE waitlist_entries
    DROP CONSTRAINT waitlist_entries_display_name_not_blank;

ALTER TABLE waitlist_entries
    RENAME COLUMN display_name TO first_name;

ALTER TABLE waitlist_entries
    ADD COLUMN last_name VARCHAR(100),
    ADD CONSTRAINT waitlist_entries_first_name_not_blank CHECK (
        first_name IS NULL OR BTRIM(first_name) <> ''
    ),
    ADD CONSTRAINT waitlist_entries_last_name_not_blank CHECK (
        last_name IS NULL OR BTRIM(last_name) <> ''
    );
