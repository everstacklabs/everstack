-- Magic-link and invitation tokens are bearer credentials that were stored in
-- plaintext, so a read of either table let anyone sign in as (or join an
-- organization as) the recipient of an outstanding link. The code now stores
-- the hex SHA-256 of the token and hashes the presented token before lookup.
-- Hash existing rows in place so links already in inboxes keep working.
-- Raw tokens are 44-char base64url, never 64 lowercase hex, so the WHERE
-- clause skips rows that are already hashed and the migration is idempotent.
SET LOCAL lock_timeout = '5s';

UPDATE magic_link_tokens
   SET token = encode(sha256(convert_to(token, 'UTF8')), 'hex')
 WHERE token !~ '^[0-9a-f]{64}$';

UPDATE invitations
   SET token = encode(sha256(convert_to(token, 'UTF8')), 'hex')
 WHERE token !~ '^[0-9a-f]{64}$';
