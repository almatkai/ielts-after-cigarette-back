ALTER TABLE users
    DROP CONSTRAINT users_role_valid;

ALTER TABLE users
    ADD CONSTRAINT users_role_valid
    CHECK (role IN ('STUDENT', 'EDITOR', 'ADMIN'));
