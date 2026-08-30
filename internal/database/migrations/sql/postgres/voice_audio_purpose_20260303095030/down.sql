-- Revert purpose CHECK constraint to exclude 'voice_audio'.
ALTER TABLE object_storage_objects DROP CONSTRAINT IF EXISTS object_storage_objects_purpose_check;
ALTER TABLE object_storage_objects ADD CONSTRAINT object_storage_objects_purpose_check
    CHECK (purpose IN ('dataset', 'artifact', 'upload', 'eval_result'));
