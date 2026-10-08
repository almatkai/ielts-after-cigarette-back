-- Blog platform: verified writers (IELTS 7.5+), writer applications,
-- blog posts with rich text bodies, and per-post image media.

ALTER TABLE users DROP CONSTRAINT users_role_valid;
ALTER TABLE users ADD CONSTRAINT users_role_valid
    CHECK (role IN ('STUDENT', 'WRITER', 'EDITOR', 'ADMIN'));

-- Applications to become a blog writer. Users attest their official IELTS
-- result (overall band 7.5+) and attach a certificate; an admin reviews the
-- application and grants the WRITER role on approval.
CREATE TABLE writer_applications (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    overall_band NUMERIC(2,1) NOT NULL,
    listening_band NUMERIC(2,1),
    reading_band NUMERIC(2,1) NOT NULL,
    writing_band NUMERIC(2,1) NOT NULL,
    speaking_band NUMERIC(2,1) NOT NULL,
    trf_number VARCHAR(64) NOT NULL,
    test_date DATE,
    exam_type VARCHAR(32),
    bio TEXT NOT NULL DEFAULT '',
    certificate_storage_key VARCHAR(500) NOT NULL,
    certificate_mime_type VARCHAR(100) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
    review_notes TEXT NOT NULL DEFAULT '',
    reviewed_by UUID REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT writer_applications_bands_valid CHECK (
        overall_band BETWEEN 0 AND 9 AND overall_band * 2 = TRUNC(overall_band * 2) AND
        overall_band >= 7.5 AND
        listening_band IS NULL OR (
            listening_band BETWEEN 0 AND 9 AND listening_band * 2 = TRUNC(listening_band * 2)
        ) AND
        reading_band BETWEEN 0 AND 9 AND reading_band * 2 = TRUNC(reading_band * 2) AND
        writing_band BETWEEN 0 AND 9 AND writing_band * 2 = TRUNC(writing_band * 2) AND
        speaking_band BETWEEN 0 AND 9 AND speaking_band * 2 = TRUNC(speaking_band * 2)
    ),
    CONSTRAINT writer_applications_trf_not_blank CHECK (BTRIM(trf_number) <> ''),
    CONSTRAINT writer_applications_exam_type_valid CHECK (
        exam_type IS NULL OR exam_type IN ('academic', 'general')
    ),
    CONSTRAINT writer_applications_status_valid CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED'))
);

CREATE INDEX writer_applications_status_idx
    ON writer_applications (status, created_at DESC);
CREATE UNIQUE INDEX writer_applications_one_pending_per_user
    ON writer_applications (user_id) WHERE status = 'PENDING';

CREATE TRIGGER writer_applications_set_updated_at
    BEFORE UPDATE ON writer_applications
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Blog posts. Body is stored as sanitized rich text HTML produced by the
-- editor; body_json keeps the editor document for future re-rendering.
CREATE TABLE blog_posts (
    id UUID PRIMARY KEY,
    author_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    slug VARCHAR(255) NOT NULL UNIQUE,
    title VARCHAR(300) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    cover_media_id UUID,
    body_html TEXT NOT NULL,
    body_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    reading_time_minutes INTEGER NOT NULL DEFAULT 1,
    status VARCHAR(20) NOT NULL DEFAULT 'DRAFT',
    published_at TIMESTAMPTZ,
    content_updated_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT blog_posts_title_not_blank CHECK (BTRIM(title) <> ''),
    CONSTRAINT blog_posts_status_valid CHECK (status IN ('DRAFT', 'PUBLISHED', 'ARCHIVED')),
    CONSTRAINT blog_posts_reading_time_valid CHECK (reading_time_minutes BETWEEN 1 AND 120),
    CONSTRAINT blog_posts_body_json_object CHECK (jsonb_typeof(body_json) = 'object')
);

CREATE INDEX blog_posts_published_idx ON blog_posts (status, published_at DESC);
CREATE INDEX blog_posts_author_idx ON blog_posts (author_id, updated_at DESC);

CREATE TRIGGER blog_posts_set_updated_at
    BEFORE UPDATE ON blog_posts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Media referenced by blog cover images and inline body images.
CREATE TABLE blog_media (
    id UUID PRIMARY KEY,
    original_name VARCHAR(255) NOT NULL,
    mime_type VARCHAR(100) NOT NULL,
    storage_key VARCHAR(500) NOT NULL UNIQUE,
    byte_size BIGINT NOT NULL,
    uploaded_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT blog_media_size_valid CHECK (byte_size > 0)
);

CREATE INDEX blog_media_created_at_idx ON blog_media (created_at DESC);

ALTER TABLE blog_posts
    ADD CONSTRAINT blog_posts_cover_media_fk
    FOREIGN KEY (cover_media_id) REFERENCES blog_media(id) ON DELETE SET NULL;
