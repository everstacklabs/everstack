# Configuration Validation System

This package provides a comprehensive configuration validation system for the Everstack LLM Gateway. It uses JSON schemas to validate configurations and provides both programmatic and gRPC API access.

## Overview

The configuration validation system is designed to:

1. **Use existing JSON schemas** - Leverages the comprehensive JSON schemas already defined in `cmd/config/gateway/schemas/`
2. **Provide gRPC API** - Offers gRPC services that can be used with gRPC Gateway for auto-generated HTTP endpoints
3. **Support multiple validation scenarios** - Handles YAML strings, configuration maps, and section-specific validation
4. **Generate validation schemas** - Provides schemas for UI forms and API documentation

## Architecture

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   JSON Schemas  │    │  Config Service  │    │  gRPC Service   │
│   (existing)    │───▶│   (validator)    │───▶│  (auto-gen HTTP)│
└─────────────────┘    └──────────────────┘    └─────────────────┘
                              │
                              ▼
                       ┌──────────────────┐
                       │  Validation      │
                       │  Results         │
                       └──────────────────┘
```

## Components

### 1. Validator (`internal/config/validator/validator.go`)

The core validation engine that:

- Loads JSON schemas from files or data
- Performs schema validation against configuration data
- Provides detailed error and warning reporting
- Supports type checking, required fields, ranges, and enums

### 2. Service (`internal/config/service.go`)

The service layer that:

- Manages schema loading and caching
- Provides high-level validation APIs
- Handles YAML parsing and conversion
- Offers schema retrieval and listing

### 3. gRPC Service (`internal/grpc/config_service.go`)

The gRPC service implementation that:

- Exposes validation functionality via gRPC
- Integrates with gRPC Gateway for HTTP endpoints
- Provides protobuf-based request/response handling

### 4. Protocol Buffers (`internal/proto/config_service.proto`)

The service definition that:

- Defines gRPC service methods
- Includes gRPC Gateway HTTP annotations
- Specifies request/response message types

## Usage

### Basic Validation

```go
// Create service
configService := config.NewService()

// Load schemas from existing files
schemaFiles := map[string]string{
    "config": "cmd/config/gateway/schemas/config.json",
    "models": "cmd/config/gateway/schemas/models.json",
}
configService.LoadSchemasFromFiles(schemaFiles)

// Validate YAML configuration
yamlConfig := `
grpc:
  server:
    port: 8089
    host: "0.0.0.0"
gateway:
  models:
    - provider: "openai"
      model: "gpt-4"
      api_key: "${OPENAI_API_KEY}"
`

result, err := configService.ValidateYAML(yamlConfig)
if err != nil {
    log.Fatal(err)
}

if !result.Valid {
    for _, err := range result.Errors {
        fmt.Printf("Error: %s - %s\n", err.Field, err.Message)
    }
}
```

### gRPC Integration

```go
// Create gRPC server
configService := config.NewService()
configService.LoadSchemasFromFiles(schemaFiles)

grpcServer := grpc.NewServer()
configproto.RegisterConfigServiceServer(grpcServer,
    grpc.NewConfigServiceServer(configService))

// With gRPC Gateway, this automatically creates HTTP endpoints:
// POST /api/v1/config/validate
// POST /api/v1/config/validate-map
// GET  /api/v1/config/schema
// GET  /api/v1/config/schemas
// GET  /api/v1/config/schemas/{name}
// POST /api/v1/config/validate-and-merge
```

### API Endpoints (Auto-generated via gRPC Gateway)

#### Validate YAML Configuration

```http
POST /api/v1/config/validate
Content-Type: application/json

{
  "yaml_config": "grpc:\n  server:\n    port: 8089\n..."
}
```

#### Validate Configuration Map

```http
POST /api/v1/config/validate-map
Content-Type: application/json

{
  "config": {
    "grpc": {
      "server": {
        "port": 8089,
        "host": "0.0.0.0"
      }
    }
  }
}
```

#### Get Validation Schema

```http
GET /api/v1/config/schema
```

#### Get Available Schemas

```http
GET /api/v1/config/schemas
```

#### Get Specific Schema

```http
GET /api/v1/config/schemas/config
```

## Validation Features

The system validates against the comprehensive JSON schemas in `cmd/config/gateway/schemas/`:

### Config Schema (`config.json`)

- gRPC server configuration
- Logging settings
- Observability (metrics, tracing, health)
- Secret manager configuration
- Database settings
- Cache configuration
- Gateway configuration
- Backup settings
- Alerts configuration
- Feature flags

### Models Schema (`models.json`)

- Provider configurations
- Model specifications
- Authentication settings
- Rate limiting
- Capabilities
- Cost information
- Metadata

## Error Reporting

Validation errors include:

- **Field path** - Exact location of the error
- **Message** - Human-readable error description
- **Code** - Machine-readable error code

Error codes include:

- `MISSING_FIELD` - Required field not provided
- `INVALID_TYPE` - Wrong data type
- `BELOW_MINIMUM` - Value below minimum
- `ABOVE_MAXIMUM` - Value above maximum
- `INVALID_ENUM` - Value not in allowed enum
- `INVALID_YAML` - YAML parsing error

## Integration with Existing System

This validation system is designed to work alongside the existing configuration loading system:

1. **File-based loading** - Continue using existing file-based configuration loading
2. **API validation** - Use this system for API endpoint validation
3. **UI validation** - Use this system for dashboard/UI validation
4. **Schema generation** - Use this system to generate UI forms and documentation

## Benefits

1. **No hardcoded validation** - Uses existing JSON schemas
2. **Consistent validation** - Same rules for files, APIs, and UI
3. **gRPC integration** - Native gRPC support with auto-generated HTTP
4. **Comprehensive coverage** - Validates all configuration aspects
5. **Detailed error reporting** - Clear, actionable error messages
6. **Schema-driven** - Validation rules defined in schemas, not code

## Example

See `examples/schema_validation_example.go` for a complete working example that demonstrates:

- Loading schemas from existing files
- Validating YAML configurations
- Handling validation errors and warnings
- Getting validation schemas
- Using the system for API/UI scenarios
