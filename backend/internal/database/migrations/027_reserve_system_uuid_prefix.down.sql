-- Reverse 027: drop the system-identity reservation on users.id.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_id_not_system_reserved;
