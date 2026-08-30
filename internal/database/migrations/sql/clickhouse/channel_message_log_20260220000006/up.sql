-- Channel message audit log (inbound/outbound messages)
-- 30-day TTL by default, used for observability and debugging
CREATE TABLE IF NOT EXISTS channel_message_log (
    tenant_id String,
    channel_config_id String,
    session_id String,
    direction Enum8('inbound' = 0, 'outbound' = 1),
    platform String,
    platform_channel_ref String,
    platform_user_id String,
    platform_user_name String,
    message_text String,
    message_ref String,
    thread_ref String,
    metadata String DEFAULT '{}',
    created_at DateTime64(3) DEFAULT now64(3)
) ENGINE = MergeTree()
PARTITION BY toYYYYMM(created_at)
ORDER BY (tenant_id, channel_config_id, created_at)
TTL toDateTime(created_at) + INTERVAL 30 DAY;
