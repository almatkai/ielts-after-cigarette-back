-- One row per user per active day (Asia/Almaty calendar). The API writes at
-- most one row per user per day (deduplicated in Redis first), so DAU/WAU/MAU
-- and retention are plain index scans instead of log crunching.
CREATE TABLE user_activity_days (
    day DATE NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT user_activity_days_pk PRIMARY KEY (day, user_id)
);

CREATE INDEX user_activity_days_user_idx ON user_activity_days (user_id, day);

-- Time-window scans for the admin analytics overview and CSV exports.
CREATE INDEX IF NOT EXISTS users_created_at_idx ON users (created_at);
CREATE INDEX IF NOT EXISTS attempts_started_at_idx ON attempts (started_at);
CREATE INDEX IF NOT EXISTS full_mock_sessions_started_at_idx ON full_mock_sessions (started_at);

-- Backfill history from the activity we already record so the charts do not
-- start empty: sign-ins (refresh sessions) and test attempts.
INSERT INTO user_activity_days (day, user_id, first_seen_at)
SELECT (seen_at AT TIME ZONE 'Asia/Almaty')::date, user_id, MIN(seen_at)
FROM (
    SELECT user_id, created_at AS seen_at FROM refresh_sessions
    UNION ALL
    SELECT user_id, started_at FROM attempts
    UNION ALL
    SELECT user_id, submitted_at FROM attempts WHERE submitted_at IS NOT NULL
) activity
GROUP BY 1, 2
ON CONFLICT DO NOTHING;
