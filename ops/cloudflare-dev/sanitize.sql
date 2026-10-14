-- Run only against the freshly restored, isolated dev database.
BEGIN;
TRUNCATE refresh_sessions, phone_verifications, user_activity_days,
         user_skill_progress, ai_provider_calls, ai_provider_rotation,
         full_mock_session_sections, full_mock_sessions, attempts CASCADE;
UPDATE users SET password_hash = '!', phone = NULL, referred_by_code = NULL;
UPDATE users
SET email = id::text || '@dev.invalid', google_sub = NULL,
    first_name = 'Dev', last_name = 'Student', source = 'dev-snapshot',
    referral_code = left(replace(id::text, '-', ''), 16)
WHERE role = 'STUDENT';
UPDATE user_profiles SET display_name = 'Dev Student', exam_date = NULL
WHERE user_id IN (SELECT id FROM users WHERE role = 'STUDENT');
COMMIT;
