-- A versioned user-owned aggregate. Row locking serializes mutations, including
-- attempt starts. JSON retains immutable past assignments and change history.
CREATE TABLE study_plans (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    revision BIGINT NOT NULL CHECK (revision > 0),
    state JSONB NOT NULL CHECK (jsonb_typeof(state) = 'object'),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- A submitted attempt is evidence for at most one practice assignment. Reviews
-- refer to their source in the aggregate and deliberately never claim evidence.
CREATE TABLE study_plan_attempts (
    assignment_id UUID PRIMARY KEY,
    plan_id UUID NOT NULL REFERENCES study_plans(id) ON DELETE CASCADE,
    attempt_id UUID NOT NULL UNIQUE REFERENCES attempts(id) ON DELETE CASCADE
);
CREATE INDEX study_plan_attempts_plan_idx ON study_plan_attempts(plan_id);
