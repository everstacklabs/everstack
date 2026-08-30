ALTER TABLE sandbox_usage_records
    DROP COLUMN IF EXISTS network_rx_bytes,
    DROP COLUMN IF EXISTS network_tx_bytes;
