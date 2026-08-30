-- Add network configuration columns for isolated function execution
-- These control network access policy for Firecracker microVMs

-- Network mode: deny (no network), whitelist (allowed hosts only), allow (full access)
ALTER TABLE functions
ADD COLUMN IF NOT EXISTS network_mode VARCHAR(20) DEFAULT 'deny' CHECK (network_mode IN ('deny', 'whitelist', 'allow'));

-- Allowed hosts for whitelist mode (hostnames or IP addresses)
ALTER TABLE functions
ADD COLUMN IF NOT EXISTS allowed_hosts TEXT[] DEFAULT '{}';

-- Add vcpus column for isolated mode (default 1)
ALTER TABLE functions
ADD COLUMN IF NOT EXISTS vcpus INTEGER DEFAULT 1 CHECK (vcpus >= 1 AND vcpus <= 8);

-- Add index for network mode filtering
CREATE INDEX IF NOT EXISTS idx_functions_network_mode ON functions(network_mode);

-- Add comment for documentation
COMMENT ON COLUMN functions.network_mode IS 'Network access policy: deny (no network), whitelist (allowed_hosts only), allow (full network)';
COMMENT ON COLUMN functions.allowed_hosts IS 'List of hostnames/IPs allowed when network_mode is whitelist';
COMMENT ON COLUMN functions.vcpus IS 'Number of virtual CPUs for isolated execution (1-8)';
