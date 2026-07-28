DROP TRIGGER IF EXISTS user_skill_progress_set_updated_at ON user_skill_progress;
DROP TRIGGER IF EXISTS refresh_sessions_set_updated_at ON refresh_sessions;
DROP TRIGGER IF EXISTS user_profiles_set_updated_at ON user_profiles;
DROP TRIGGER IF EXISTS users_set_updated_at ON users;
DROP FUNCTION IF EXISTS set_updated_at();
DROP TABLE IF EXISTS user_skill_progress;
DROP TABLE IF EXISTS refresh_sessions;
DROP TABLE IF EXISTS user_profiles;
DROP TABLE IF EXISTS users;

