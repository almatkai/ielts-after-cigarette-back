ALTER TABLE waitlist_entries
    ADD COLUMN google_sub VARCHAR(255);

CREATE UNIQUE INDEX waitlist_entries_google_sub_unique
    ON waitlist_entries (google_sub)
    WHERE google_sub IS NOT NULL;
