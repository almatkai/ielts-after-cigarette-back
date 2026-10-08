CREATE TABLE ai_providers (
 id UUID PRIMARY KEY,
 name VARCHAR(120) NOT NULL,
 endpoint VARCHAR(1000) NOT NULL,
 model VARCHAR(200) NOT NULL,
 speaking_model VARCHAR(200) NOT NULL DEFAULT '',
 scopes TEXT[] NOT NULL DEFAULT ARRAY['assistant','writing','speaking'],
 enabled BOOLEAN NOT NULL DEFAULT false,
 priority INTEGER NOT NULL DEFAULT 100,
 timeout_seconds INTEGER NOT NULL DEFAULT 20,
 key_ciphertext BYTEA NOT NULL,
 revision BIGINT NOT NULL DEFAULT 1,
 created_by UUID REFERENCES users(id) ON DELETE SET NULL,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 CONSTRAINT ai_provider_priority CHECK (priority BETWEEN 0 AND 10000),
 CONSTRAINT ai_provider_timeout CHECK (timeout_seconds BETWEEN 5 AND 60),
 CONSTRAINT ai_provider_scopes CHECK (cardinality(scopes)>0 AND scopes <@ ARRAY['assistant','writing','speaking']),
 CONSTRAINT ai_provider_key CHECK (octet_length(key_ciphertext) BETWEEN 38 AND 16384)
);
CREATE INDEX ai_providers_chain_idx ON ai_providers (priority,id) WHERE enabled;
