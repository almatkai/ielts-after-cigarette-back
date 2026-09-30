-- The mistake bank pages submitted attempts by skill, newest first.
CREATE INDEX attempts_mistake_bank_idx
    ON attempts (user_id, material_type, submitted_at DESC, id DESC)
    WHERE status = 'SUBMITTED';
