ALTER TABLE blog_posts DROP CONSTRAINT IF EXISTS blog_posts_cover_media_fk;
DROP TABLE IF EXISTS blog_media;
DROP TABLE IF EXISTS blog_posts;
DROP TRIGGER IF EXISTS blog_posts_set_updated_at ON blog_posts;
DROP TABLE IF EXISTS writer_applications;
ALTER TABLE users DROP CONSTRAINT users_role_valid;
ALTER TABLE users ADD CONSTRAINT users_role_valid
    CHECK (role IN ('STUDENT', 'EDITOR', 'ADMIN'));
