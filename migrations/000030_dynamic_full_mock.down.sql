-- Rolling back would discard generated exam history. Refuse until an operator
-- explicitly migrates it to legacy mocks instead of silently deleting results.
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM full_mock_sessions WHERE mock_test_id IS NULL) THEN
        RAISE EXCEPTION 'Cannot roll back dynamic full mocks while generated sessions exist';
    END IF;
END $$;
DROP INDEX attempts_completed_material_idx;
DROP INDEX full_mock_sessions_generated_active_user_idx;
ALTER TABLE full_mock_sessions
    DROP CONSTRAINT full_mock_sessions_metadata_valid,
    DROP COLUMN duration_minutes,
    DROP COLUMN title,
    DROP COLUMN exam_type,
    ALTER COLUMN mock_test_id SET NOT NULL;
