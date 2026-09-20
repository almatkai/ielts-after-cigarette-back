CREATE TABLE writing_media (
    id UUID PRIMARY KEY,
    kind VARCHAR(16) NOT NULL DEFAULT 'image',
    original_name VARCHAR(255) NOT NULL,
    mime_type VARCHAR(100) NOT NULL,
    storage_key VARCHAR(500) NOT NULL UNIQUE,
    byte_size BIGINT NOT NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT writing_media_kind_valid CHECK (kind = 'image'),
    CONSTRAINT writing_media_size_valid CHECK (byte_size > 0)
);

CREATE INDEX writing_media_created_at_idx ON writing_media (created_at DESC);
