-- Durable upload lifecycle, exact-once quota reservations, and transition
-- evidence. object_storage_objects remains the registry of ready objects.

ALTER TABLE object_storage_usage
    ADD COLUMN IF NOT EXISTS reserved_bytes BIGINT NOT NULL DEFAULT 0 CHECK (reserved_bytes >= 0),
    ADD COLUMN IF NOT EXISTS reserved_object_count BIGINT NOT NULL DEFAULT 0 CHECK (reserved_object_count >= 0);

CREATE TABLE IF NOT EXISTS object_storage_uploads (
    id                       VARCHAR(255) PRIMARY KEY,
    tenant_id                VARCHAR(255) NOT NULL,
    -- Empty config IDs are reserved for the managed Everstack Storage
    -- connection introduced by the next roadmap slice.
    config_id                VARCHAR(255) NOT NULL DEFAULT '',
    key                      TEXT NOT NULL,
    filename                 VARCHAR(512) NOT NULL DEFAULT '',
    content_type             VARCHAR(255) NOT NULL DEFAULT 'application/octet-stream',
    expected_size_bytes      BIGINT NOT NULL CHECK (expected_size_bytes >= 0),
    expected_checksum_sha256 VARCHAR(64) NOT NULL DEFAULT '',
    actual_size_bytes        BIGINT NOT NULL DEFAULT 0 CHECK (actual_size_bytes >= 0),
    actual_checksum_sha256   VARCHAR(64) NOT NULL DEFAULT '',
    purpose                  VARCHAR(50) NOT NULL CHECK (
        purpose IN ('dataset', 'artifact', 'upload', 'eval_result', 'voice_audio')
    ),
    reference_id             VARCHAR(255) NOT NULL DEFAULT '',
    reference_type           VARCHAR(100) NOT NULL DEFAULT '',
    metadata                 JSONB NOT NULL DEFAULT '{}',
    idempotency_key          VARCHAR(255) NOT NULL CHECK (idempotency_key <> ''),
    request_fingerprint      VARCHAR(255) NOT NULL CHECK (request_fingerprint <> ''),
    state                    VARCHAR(32) NOT NULL CHECK (
        state IN (
            'pending',
            'transferred',
            'verifying',
            'ready',
            'failed',
            'quarantined',
            'deleting',
            'deleted'
        )
    ),
    reservation_state        VARCHAR(32) NOT NULL CHECK (
        reservation_state IN ('reserved', 'committed', 'released')
    ),
    last_error_code          VARCHAR(64) NOT NULL DEFAULT '',
    attempt_count            BIGINT NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    last_error_at            TIMESTAMPTZ,
    next_attempt_at          TIMESTAMPTZ,
    multipart_upload_id      TEXT NOT NULL DEFAULT '',
    expires_at               TIMESTAMPTZ NOT NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT object_storage_uploads_tenant_idempotency_unique
        UNIQUE (tenant_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_object_storage_uploads_tenant_state
    ON object_storage_uploads (tenant_id, state, updated_at);

CREATE INDEX IF NOT EXISTS idx_object_storage_uploads_reconcile
    ON object_storage_uploads (state, next_attempt_at, expires_at)
    WHERE state IN ('pending', 'transferred', 'verifying', 'failed', 'quarantined', 'deleting');

CREATE TABLE IF NOT EXISTS object_storage_upload_events (
    sequence    BIGSERIAL PRIMARY KEY,
    tenant_id   VARCHAR(255) NOT NULL,
    object_id   VARCHAR(255) NOT NULL REFERENCES object_storage_uploads(id) ON DELETE CASCADE,
    from_state  VARCHAR(32) NOT NULL DEFAULT '',
    to_state    VARCHAR(32) NOT NULL CHECK (
        to_state IN (
            'pending',
            'transferred',
            'verifying',
            'ready',
            'failed',
            'quarantined',
            'deleting',
            'deleted'
        )
    ),
    reason_code VARCHAR(64) NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_object_storage_upload_events_object
    ON object_storage_upload_events (tenant_id, object_id, sequence);

-- Existing registry rows are already visible to clients, so preserve them as
-- committed ready records (or released deleted records). They gain a durable
-- lifecycle without changing their object IDs, URLs, or accounting totals.
INSERT INTO object_storage_uploads (
    id,
    tenant_id,
    config_id,
    key,
    filename,
    content_type,
    expected_size_bytes,
    expected_checksum_sha256,
    actual_size_bytes,
    actual_checksum_sha256,
    purpose,
    reference_id,
    reference_type,
    metadata,
    idempotency_key,
    request_fingerprint,
    state,
    reservation_state,
    expires_at,
    created_at,
    updated_at
)
SELECT
    id,
    tenant_id,
    config_id,
    key,
    filename,
    content_type,
    size_bytes,
    checksum_sha256,
    size_bytes,
    checksum_sha256,
    purpose,
    reference_id,
    reference_type,
    metadata,
    'legacy:' || id,
    'legacy:' || id,
    CASE WHEN deleted_at IS NULL THEN 'ready' ELSE 'deleted' END,
    CASE WHEN deleted_at IS NULL THEN 'committed' ELSE 'released' END,
    created_at,
    created_at,
    COALESCE(deleted_at, created_at)
FROM object_storage_objects
ON CONFLICT (id) DO NOTHING;

INSERT INTO object_storage_upload_events (
    tenant_id,
    object_id,
    from_state,
    to_state,
    reason_code,
    created_at
)
SELECT
    tenant_id,
    id,
    '',
    state,
    'legacy_backfill',
    created_at
FROM object_storage_uploads
WHERE idempotency_key LIKE 'legacy:%'
  AND NOT EXISTS (
      SELECT 1
      FROM object_storage_upload_events existing
      WHERE existing.tenant_id = object_storage_uploads.tenant_id
        AND existing.object_id = object_storage_uploads.id
  );

-- Install dormant tenant policies. Arming remains a separate migration after
-- every lifecycle call runs inside tenant-scoped database transactions.
DROP POLICY IF EXISTS tenant_isolation ON object_storage_uploads;
CREATE POLICY tenant_isolation ON object_storage_uploads
    FOR ALL
    USING (everstack.tenant_matches(tenant_id::text))
    WITH CHECK (everstack.tenant_matches(tenant_id::text));

DROP POLICY IF EXISTS tenant_isolation ON object_storage_upload_events;
CREATE POLICY tenant_isolation ON object_storage_upload_events
    FOR ALL
    USING (everstack.tenant_matches(tenant_id::text))
    WITH CHECK (everstack.tenant_matches(tenant_id::text));
