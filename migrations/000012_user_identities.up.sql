CREATE TABLE user_identities (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    provider_sub TEXT NOT NULL,
    email TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_identities_provider_sub_unique UNIQUE (provider, provider_sub)
);

CREATE INDEX user_identities_user_idx ON user_identities (user_id);

INSERT INTO user_identities (id, user_id, provider, provider_sub, email)
SELECT gen_random_uuid(), id, 'google', google_sub, email
FROM users
WHERE google_sub IS NOT NULL AND google_sub <> '';
