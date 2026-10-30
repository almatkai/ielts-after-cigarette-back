CREATE TABLE study_learning_content_metadata (
    kind TEXT NOT NULL CHECK (kind IN ('drill','material')),
    reference_id TEXT NOT NULL,
    version TEXT NOT NULL,
    section TEXT NOT NULL CHECK (section IN ('reading','listening','writing','speaking')),
    family_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('draft','reviewed','withdrawn')),
    content_hash TEXT NOT NULL,
    revision BIGINT NOT NULL CHECK (revision > 0),
    review_notes TEXT NOT NULL,
    reviewer_id UUID NOT NULL REFERENCES users(id),
    reviewed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    checklist JSONB NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (kind,reference_id,version),
    CHECK (status <> 'reviewed' OR family_id <> '')
);
CREATE TABLE study_learning_content_reviews (
    id BIGSERIAL PRIMARY KEY,
    kind TEXT NOT NULL,
    reference_id TEXT NOT NULL,
    version TEXT NOT NULL,
    revision BIGINT NOT NULL,
    decision JSONB NOT NULL,
    reviewer_id UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (kind,reference_id,version,revision)
);
