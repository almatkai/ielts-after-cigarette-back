CREATE TABLE ai_provider_routing (
 singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
 mode TEXT NOT NULL DEFAULT 'sequential' CHECK (mode = 'sequential'),
 max_parallel INTEGER NOT NULL DEFAULT 1 CHECK (max_parallel BETWEEN 1 AND 4),
 hedge_delay_ms INTEGER NOT NULL DEFAULT 0 CHECK (hedge_delay_ms BETWEEN 0 AND 10000),
 provider_ids UUID[] NOT NULL DEFAULT '{}',
 revision BIGINT NOT NULL DEFAULT 1,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO ai_provider_routing(singleton) VALUES (true);

-- Shared round-robin counters: multiple API/worker instances use one order.
CREATE TABLE ai_provider_rotation (
 priority INTEGER NOT NULL CHECK (priority BETWEEN 0 AND 10000),
 purpose VARCHAR(16) NOT NULL CHECK (purpose IN ('assistant','writing','speaking')),
 cursor BIGINT NOT NULL DEFAULT 0 CHECK (cursor >= 0),
 PRIMARY KEY (priority,purpose)
);

-- Operational metadata only: no credentials, endpoints, prompts or answers.
CREATE TABLE ai_provider_calls (
 id UUID PRIMARY KEY,
 run_id UUID NOT NULL,
 provider_id UUID NOT NULL,
 provider_name VARCHAR(120) NOT NULL,
 model VARCHAR(200) NOT NULL,
 purpose VARCHAR(16) NOT NULL CHECK (purpose IN ('assistant','writing','speaking','test')),
 from_env BOOLEAN NOT NULL,
 started_at TIMESTAMPTZ NOT NULL,
 duration_ms BIGINT NOT NULL CHECK (duration_ms >= 0),
 first_response_ms BIGINT CHECK (first_response_ms >= 0),
 first_token_ms BIGINT CHECK (first_token_ms >= 0),
 outcome VARCHAR(16) NOT NULL CHECK (outcome IN ('success','failure','timeout','cancelled')),
 won BOOLEAN NOT NULL,
 trigger VARCHAR(24) NOT NULL,
 mode VARCHAR(16) NOT NULL,
 code VARCHAR(64) NOT NULL DEFAULT '',
 http_status INTEGER NOT NULL DEFAULT 0,
 request_id VARCHAR(128) NOT NULL DEFAULT ''
);
CREATE INDEX ai_provider_calls_window_idx ON ai_provider_calls (started_at DESC);
CREATE INDEX ai_provider_calls_model_idx ON ai_provider_calls (provider_id,model,purpose,started_at DESC);
