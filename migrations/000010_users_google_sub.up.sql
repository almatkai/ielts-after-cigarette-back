ALTER TABLE users
    ADD COLUMN google_sub TEXT;

CREATE UNIQUE INDEX users_google_sub_unique
    ON users (google_sub)
    WHERE google_sub IS NOT NULL;
