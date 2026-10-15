-- Stores Kazakh and English translations of question review content.
-- The Russian source text stays in reading_questions/listening_questions
-- (explanation column, content->>'hint'); this table carries translations
-- keyed by question so future languages do not churn the hot tables.
CREATE TABLE question_translations (
    question_id UUID NOT NULL,
    skill TEXT NOT NULL CHECK (skill IN ('reading', 'listening')),
    lang TEXT NOT NULL CHECK (lang IN ('kk', 'en')),
    explanation TEXT NOT NULL DEFAULT '',
    hint TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT question_translations_pkey PRIMARY KEY (question_id, lang)
);

CREATE INDEX question_translations_question_idx
    ON question_translations (question_id);
