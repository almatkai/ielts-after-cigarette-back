ALTER TABLE study_learning_content_reviews
    DROP CONSTRAINT IF EXISTS study_learning_content_reviews_reviewer_id_fkey;
ALTER TABLE study_learning_content_reviews
    ADD CONSTRAINT study_learning_content_reviews_reviewer_id_fkey
    FOREIGN KEY (reviewer_id) REFERENCES users(id);
ALTER TABLE study_learning_content_reviews
    ALTER COLUMN reviewer_id SET NOT NULL;

ALTER TABLE study_learning_content_metadata
    DROP CONSTRAINT IF EXISTS study_learning_content_metadata_reviewer_id_fkey;
ALTER TABLE study_learning_content_metadata
    ADD CONSTRAINT study_learning_content_metadata_reviewer_id_fkey
    FOREIGN KEY (reviewer_id) REFERENCES users(id);
ALTER TABLE study_learning_content_metadata
    ALTER COLUMN reviewer_id SET NOT NULL;
