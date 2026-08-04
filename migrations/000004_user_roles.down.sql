ALTER TABLE users
    DROP CONSTRAINT users_role_valid;

UPDATE users
SET role = 'STUDENT'
WHERE role <> 'STUDENT';

ALTER TABLE users
    ADD CONSTRAINT users_role_valid
    CHECK (role IN ('STUDENT'));
