# Dual Configuration Architecture

This document explains how Everstack supports two distinct usage patterns:

1. **Standalone Gateway** - Users provide their own configuration file
2. **Everstack Platform** - Configuration managed through Everstack Cloud or self-hosted platform

## Usage Patterns

### 1. Standalone Gateway Usage

When users run the gateway independently (not through Everstack platform):

```bash
# Users provide their own gateway.yaml configuration
mf serve --config gateway.yaml
```

**Characteristics:**

- User manages their own configuration file
- Self-contained deployment
- No dependency on Everstack platform
- Traditional file-based configuration

### 2. Everstack Platform Usage

When users deploy through Everstack Cloud or self-hosted platform:

```bash
# Platform manages configuration automatically
mf serve --platform-config
```

**Characteristics:**

- Platform manages gateway configuration
- Configuration stored in platform database
- Real-time configuration updates
- Multi-tenant support
- Centralized management

## Architecture Components

### 1. ConfigProvider Interface

```go
type ConfigProvider interface {
    LoadConfig() (*Config, error)
    SaveConfig(*Config) error
    GetSource() ConfigSource
    Validate() error
}
```

### 2. Configuration Sources

#### FileConfigProvider

- **Purpose**: Loads configuration from user-provided YAML files
- **Usage**: Standalone gateway deployments
- **Validation**: Ensures file exists and is readable
- **Saving**: Not supported (read-only)

#### PlatformConfigProvider

- **Purpose**: Loads configuration from Everstack platform
- **Usage**: Platform-managed gateway deployments
- **Validation**: Requires platform connection and credentials
- **Saving**: Supports saving configuration changes through platform

### 3. ConfigManager

The `ConfigManager` orchestrates configuration loading based on deployment mode:

```go
type ConfigManager struct {
    providers []ConfigProvider
    defaults  *DefaultConfigs
    mode      DeploymentMode
}
```

## Deployment Modes

### Standalone Mode

- Single configuration source (file)
- User-managed configuration
- No platform dependencies

### Platform Mode

- Platform-managed configuration
- Real-time updates
- Multi-tenant support
- Centralized management

## Configuration Flow

### Standalone Gateway Flow

```
User creates gateway.yaml
    ↓
FileConfigProvider loads config
    ↓
Validate against embedded defaults
    ↓
Start gateway with file configuration
```

### Platform-Managed Gateway Flow

```
Platform creates gateway instance
    ↓
PlatformConfigProvider connects to platform
    ↓
Load configuration from platform database
    ↓
Validate against embedded defaults
    ↓
Start gateway with platform configuration
    ↓
Listen for configuration updates from platform
```

## Environment Variables

### Standalone Mode

```bash
# Traditional file-based configuration
export MF_CONFIG_FILE_PATH="/path/to/gateway.yaml"
mf serve --config gateway.yaml
```

### Platform Mode

```bash
# Platform-managed configuration
export MF_PLATFORM_ENABLED=true
export MF_PLATFORM_URL="https://platform.everstack.com"
export MF_PLATFORM_API_KEY="your-api-key"
export MF_GATEWAY_INSTANCE_ID="gateway-123"
mf serve --platform-config
```

## Platform Integration

### Gateway Registration

When a gateway starts in platform mode:

1. **Register with Platform**: Gateway registers itself with the Everstack platform
2. **Receive Configuration**: Platform provides initial configuration
3. **Establish Connection**: Gateway establishes persistent connection for updates
4. **Listen for Changes**: Gateway listens for configuration updates

### Configuration Updates

The platform can update gateway configuration in real-time:

1. **User Changes Config**: User updates configuration in Everstack dashboard
2. **Platform Notifies Gateway**: Platform sends configuration update to gateway
3. **Gateway Reloads**: Gateway reloads configuration without restart
4. **Validation**: Gateway validates new configuration before applying

## Implementation Status

### ✅ Completed

- [x] ConfigProvider interface
- [x] FileConfigProvider implementation
- [x] ConfigManager with mode detection
- [x] Integration with existing serve command
- [x] Environment variable support
- [x] Validation and error handling

### 🚧 In Progress

- [ ] PlatformConfigProvider implementation
- [ ] Platform API integration
- [ ] Real-time configuration updates
- [ ] Gateway registration protocol

### 📋 Planned

- [ ] Platform dashboard for gateway management
- [ ] Multi-tenant configuration isolation
- [ ] Configuration templates and presets
- [ ] Gateway health monitoring
- [ ] Configuration backup/restore

## Platform Database Schema

The Everstack platform will store gateway configurations:

```sql
-- Organizations
CREATE TABLE organizations (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Gateway instances
CREATE TABLE gateway_instances (
    id UUID PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES organizations(id),
    name VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'inactive',
    last_seen TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Gateway configurations
CREATE TABLE gateway_configurations (
    id UUID PRIMARY KEY,
    gateway_id UUID NOT NULL REFERENCES gateway_instances(id),
    config_data JSONB NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    is_active BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    created_by UUID
);

-- Configuration history
CREATE TABLE configuration_history (
    id UUID PRIMARY KEY,
    config_id UUID NOT NULL REFERENCES gateway_configurations(id),
    config_data JSONB NOT NULL,
    version INTEGER NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    created_by UUID
);
```

## Migration Strategy

### Phase 1: Standalone Only (Current)

- Users deploy standalone gateways
- File-based configuration only
- No platform dependencies

### Phase 2: Platform Introduction (Next)

- Everstack platform becomes available
- Users can choose standalone or platform mode
- Platform provides additional features

### Phase 3: Platform-First (Future)

- Platform becomes the primary deployment method
- Standalone mode remains for edge cases
- Enhanced platform features

### Phase 4: Platform-Only (Distant Future)

- Platform becomes the standard deployment method
- Standalone mode for special use cases only

## Benefits

### Standalone Gateway

1. **Simplicity**: No external dependencies
2. **Control**: Full control over configuration
3. **Privacy**: No data leaves user's infrastructure
4. **Offline**: Works without internet connection

### Platform-Managed Gateway

1. **Ease of Use**: No configuration file management
2. **Real-time Updates**: Change configuration instantly
3. **Centralized Management**: Manage multiple gateways
4. **Advanced Features**: Templates, monitoring, analytics
5. **Collaboration**: Multiple users can manage configuration

## Security Considerations

### Standalone Gateway

- Configuration stored locally
- No network exposure of configuration
- User controls all access

### Platform-Managed Gateway

- Encrypted communication with platform
- User authentication and authorization
- Audit logging for all changes
- Data encryption at rest

## Best Practices

### Standalone Deployment

1. Use version control for configuration files
2. Implement configuration validation
3. Use environment variables for secrets
4. Regular configuration backups

### Platform Deployment

1. Use platform's built-in security features
2. Implement proper access controls
3. Monitor gateway health
4. Use configuration templates for consistency
