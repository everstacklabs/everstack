-- A SHA-256 cannot be reversed, and code from before up.sql looks tokens up
-- in plaintext, so no hashed row would ever match again. Delete the
-- outstanding magic links (users request a new one) and pending invitations
-- (admins re-invite). Accepted invitations are kept as membership history.
SET LOCAL lock_timeout = '5s';

DELETE FROM magic_link_tokens;

DELETE FROM invitations WHERE accepted_at IS NULL;
