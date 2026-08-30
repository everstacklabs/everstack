-- Rollback self-hosted authentication tables
-- WARNING: This will delete all user data!

DROP TABLE IF EXISTS auth_config;
DROP TABLE IF EXISTS magic_link_tokens;
DROP TABLE IF EXISTS invitations;
DROP TABLE IF EXISTS user_credentials;
DROP TABLE IF EXISTS organization_members;
DROP TABLE IF EXISTS organizations;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
