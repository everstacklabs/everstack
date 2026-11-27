-- Second pass of auth_hash_link_tokens_20261024171406. During that rolling
-- deploy, pods on the previous release could still insert plaintext tokens
-- after the first pass ran; the code carried a plaintext lookup fallback for
-- one release to keep those links working. This release drops the fallback,
-- so hash any such rows first. Raw tokens are 44-char base64url, never 64
-- lowercase hex, so already-hashed rows are skipped and this is idempotent.
SET LOCAL lock_timeout = '5s';

UPDATE magic_link_tokens
   SET token = encode(sha256(convert_to(token, 'UTF8')), 'hex')
 WHERE token !~ '^[0-9a-f]{64}$';

UPDATE invitations
   SET token = encode(sha256(convert_to(token, 'UTF8')), 'hex')
 WHERE token !~ '^[0-9a-f]{64}$';
