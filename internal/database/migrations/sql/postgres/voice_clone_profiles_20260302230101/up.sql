-- Create voice_clone_profiles table for managing cloned voice profiles
CREATE TABLE IF NOT EXISTS voice_clone_profiles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    reference_audio_object_id TEXT,
    reference_audio_duration_seconds DOUBLE PRECISION NOT NULL DEFAULT 0,
    reference_text TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT 'qwen',
    model TEXT NOT NULL DEFAULT 'qwen3-tts-vc',
    provider_voice_id TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

-- Unique constraint on org + name (excluding soft-deleted)
CREATE UNIQUE INDEX IF NOT EXISTS idx_voice_clone_profiles_org_name
    ON voice_clone_profiles(org_id, name) WHERE deleted_at IS NULL;

-- Index for listing by org
CREATE INDEX IF NOT EXISTS idx_voice_clone_profiles_org
    ON voice_clone_profiles(org_id) WHERE deleted_at IS NULL;

COMMENT ON TABLE voice_clone_profiles IS 'Stores voice clone profiles created from reference audio for TTS';
COMMENT ON COLUMN voice_clone_profiles.provider_voice_id IS 'The voice ID returned by the provider (e.g. DashScope) after cloning';
COMMENT ON COLUMN voice_clone_profiles.reference_audio_object_id IS 'Object storage key for the uploaded reference audio';
