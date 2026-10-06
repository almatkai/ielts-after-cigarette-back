CREATE TABLE IF NOT EXISTS system_daily_limits (
    id INT PRIMARY KEY DEFAULT 1,
    assistant_limit INT NOT NULL DEFAULT 100,
    guest_assistant_limit INT NOT NULL DEFAULT 15,
    writing_limit INT NOT NULL DEFAULT 25,
    speaking_limit INT NOT NULL DEFAULT 25,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT system_daily_limits_single_row CHECK (id = 1),
    CONSTRAINT system_daily_limits_positive CHECK (
        assistant_limit >= 0 AND
        guest_assistant_limit >= 0 AND
        writing_limit >= 0 AND
        speaking_limit >= 0
    )
);

INSERT INTO system_daily_limits (id, assistant_limit, guest_assistant_limit, writing_limit, speaking_limit)
VALUES (1, 100, 15, 25, 25)
ON CONFLICT (id) DO NOTHING;
