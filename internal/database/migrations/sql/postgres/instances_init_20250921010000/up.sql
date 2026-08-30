-- Create instances table for tamper-evident instance identity
CREATE SCHEMA IF NOT EXISTS system;

-- write your UP migration SQL here
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";


CREATE TYPE system.instance_status AS ENUM (
    'uninitialized',
    'prending',
    'active',
    'failed'
);

CREATE TABLE IF NOT EXISTS system.instances (
    id                 UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    instance_id_hash   TEXT        NOT NULL UNIQUE,
    signed_payload     JSONB       NOT NULL,
    instance_signature          TEXT        NOT NULL,
    instance_kid                TEXT        NOT NULL,
    instance_status             TEXT        NOT NULL DEFAULT 'uninitialized',
    retry_count        INT         NOT NULL DEFAULT 0,
    last_attempt_at    TIMESTAMPTZ,
    next_attempt_at    TIMESTAMPTZ,
    ip_last_seen       INET,
    ip_activation_count_24h INT NOT NULL DEFAULT 0,
    error_reason       TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_instances_status_next_attempt
    ON system.instances (instance_status, next_attempt_at);

