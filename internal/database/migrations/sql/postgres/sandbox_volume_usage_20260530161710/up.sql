-- Volume storage metering: track measured usage for usage-based billing.
--
-- size_bytes (existing) is reused as an optional capacity quota (0 = unlimited).
-- used_bytes is the last measured number of bytes stored under the volume's
-- object-storage prefix; usage_measured_at is when that measurement was taken.
-- The hourly volume metering sweep updates both and bills GiB-hours of
-- used_bytes (volumes have no free allowance; the 20 GiB free is root-disk only).

ALTER TABLE sandbox_volumes
    ADD COLUMN IF NOT EXISTS used_bytes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS usage_measured_at TIMESTAMPTZ;
