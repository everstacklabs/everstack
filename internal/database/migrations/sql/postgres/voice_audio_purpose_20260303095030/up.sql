-- Add 'voice_audio' to the allowed purpose values for object_storage_objects.
ALTER TABLE object_storage_objects DROP CONSTRAINT IF EXISTS object_storage_objects_purpose_check;
ALTER TABLE object_storage_objects ADD CONSTRAINT object_storage_objects_purpose_check
    CHECK (purpose IN ('dataset', 'artifact', 'upload', 'eval_result', 'voice_audio'));
