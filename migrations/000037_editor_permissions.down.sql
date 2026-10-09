ALTER TABLE users DROP CONSTRAINT IF EXISTS users_admin_permissions_valid;
ALTER TABLE users DROP COLUMN IF EXISTS admin_permissions;
