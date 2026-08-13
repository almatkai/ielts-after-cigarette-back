DROP TABLE IF EXISTS listening_questions;
DROP TABLE IF EXISTS listening_question_groups;
DROP TABLE IF EXISTS listening_parts;
ALTER TABLE IF EXISTS listening_tests DROP CONSTRAINT IF EXISTS listening_tests_published_version_fk;
ALTER TABLE IF EXISTS listening_tests DROP CONSTRAINT IF EXISTS listening_tests_current_version_fk;
DROP TABLE IF EXISTS listening_test_versions;
DROP TABLE IF EXISTS listening_media;
DROP TABLE IF EXISTS listening_tests;
