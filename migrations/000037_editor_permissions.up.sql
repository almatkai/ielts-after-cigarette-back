ALTER TABLE users
    ADD COLUMN admin_permissions TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE users
    ADD CONSTRAINT users_admin_permissions_valid CHECK (
        admin_permissions <@ ARRAY['BLOG_MODERATOR', 'CONTENT_EDITOR']::TEXT[]
    );

-- Preserve existing editor access while allowing administrators to split it
-- into narrower, independently assignable capabilities.
UPDATE users
SET admin_permissions = ARRAY['BLOG_MODERATOR', 'CONTENT_EDITOR']::TEXT[],
    token_version = token_version + 1
WHERE role = 'EDITOR';
