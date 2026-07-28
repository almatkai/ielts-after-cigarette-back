CREATE TABLE users (
    id UUID PRIMARY KEY,
    email VARCHAR(254) NOT NULL,
    password_hash TEXT NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT 'STUDENT',
    terms_accepted_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT users_email_normalized CHECK (email = LOWER(BTRIM(email))),
    CONSTRAINT users_role_valid CHECK (role IN ('STUDENT'))
);

CREATE UNIQUE INDEX users_email_unique ON users (LOWER(email));

CREATE TABLE user_profiles (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    display_name VARCHAR(100) NOT NULL,
    current_band NUMERIC(2,1),
    target_band NUMERIC(2,1),
    exam_date DATE,
    exam_type VARCHAR(32),
    timezone VARCHAR(64) NOT NULL DEFAULT 'UTC',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT user_profiles_display_name_not_blank CHECK (BTRIM(display_name) <> ''),
    CONSTRAINT user_profiles_current_band_valid CHECK (
        current_band IS NULL OR
        (current_band BETWEEN 0 AND 9 AND current_band * 2 = TRUNC(current_band * 2))
    ),
    CONSTRAINT user_profiles_target_band_valid CHECK (
        target_band IS NULL OR
        (target_band BETWEEN 0 AND 9 AND target_band * 2 = TRUNC(target_band * 2))
    ),
    CONSTRAINT user_profiles_exam_type_valid CHECK (
        exam_type IS NULL OR exam_type IN ('academic', 'general')
    )
);

CREATE TABLE refresh_sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    replaced_by UUID REFERENCES refresh_sessions(id),
    user_agent VARCHAR(512),
    ip_address VARCHAR(45),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT refresh_sessions_expiry_valid CHECK (expires_at > created_at)
);

CREATE UNIQUE INDEX refresh_sessions_token_hash_unique ON refresh_sessions (token_hash);
CREATE INDEX refresh_sessions_user_active_idx
    ON refresh_sessions (user_id, expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE user_skill_progress (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    skill VARCHAR(16) NOT NULL,
    estimated_band NUMERIC(2,1),
    accuracy_percent NUMERIC(5,2),
    completed_tasks INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT user_skill_progress_user_skill_unique UNIQUE (user_id, skill),
    CONSTRAINT user_skill_progress_skill_valid CHECK (
        skill IN ('listening', 'reading', 'writing', 'speaking')
    ),
    CONSTRAINT user_skill_progress_band_valid CHECK (
        estimated_band IS NULL OR
        (estimated_band BETWEEN 0 AND 9 AND estimated_band * 2 = TRUNC(estimated_band * 2))
    ),
    CONSTRAINT user_skill_progress_accuracy_valid CHECK (
        accuracy_percent IS NULL OR accuracy_percent BETWEEN 0 AND 100
    ),
    CONSTRAINT user_skill_progress_completed_tasks_valid CHECK (completed_tasks >= 0)
);

CREATE INDEX user_skill_progress_user_idx ON user_skill_progress (user_id);

CREATE FUNCTION set_updated_at() RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER users_set_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER user_profiles_set_updated_at
    BEFORE UPDATE ON user_profiles
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER refresh_sessions_set_updated_at
    BEFORE UPDATE ON refresh_sessions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER user_skill_progress_set_updated_at
    BEFORE UPDATE ON user_skill_progress
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
