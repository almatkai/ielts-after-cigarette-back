-- Rollback requires removing guest data explicitly first; never delete exam work
-- or relabel guest actors as registered students during a schema rollback.
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM users WHERE role = 'GUEST' OR status = 'GUEST') THEN
        RAISE EXCEPTION 'Remove guest data explicitly before rolling back guest trials';
    END IF;
END $$;
DROP TABLE guest_trials;
ALTER TABLE users DROP CONSTRAINT users_referral_code_required_for_leads;
ALTER TABLE users ADD CONSTRAINT users_referral_code_required_for_leads CHECK (status = 'REGISTERED' OR referral_code IS NOT NULL);
ALTER TABLE users DROP CONSTRAINT users_guest_status_valid;
ALTER TABLE users DROP CONSTRAINT users_status_valid;
ALTER TABLE users ADD CONSTRAINT users_status_valid CHECK (status IN ('WAITING', 'INVITED', 'REGISTERED'));
ALTER TABLE users DROP CONSTRAINT users_role_valid;
ALTER TABLE users ADD CONSTRAINT users_role_valid CHECK (role IN ('STUDENT', 'EDITOR', 'ADMIN'));
