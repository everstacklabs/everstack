# License Monitor Service

The License Monitor Service is an internal gateway service that tracks license state, monitors usage limits, and enforces feature gates based on the current license tier.

## Features

### 1. **License State Monitoring**

- Periodically checks license validity and expiration status
- Caches license state in memory for fast access
- Provides warnings before license expiry (default: 7 days)
- Automatically locks the gateway if license becomes invalid

### 2. **Usage Tracking**

Tracks real-time usage metrics:

- **RPS** (Requests Per Second)
- **RPM** (Requests Per Minute)
- **RPH** (Requests Per Hour)
- **Total Requests** (Monthly counter)

Usage limits are enforced according to the license tier:

- **Free**: 60 RPM, 10,000 requests/month
- **Basic**: 600 RPM, 100,000 requests/month
- **Pro**: 6,000 RPM, 1,000,000 requests/month
- **Enterprise**: Unlimited

### 3. **Feature Gates**

Controls access to features based on license tier:

| Feature             | Free | Basic | Pro | Enterprise |
| ------------------- | ---- | ----- | --- | ---------- |
| Core API            | ✓    | ✓     | ✓   | ✓          |
| Advanced Analytics  | ✗    | ✗     | ✓   | ✓          |
| Custom Integrations | ✗    | ✗     | ✓   | ✓          |
| SSO                 | ✗    | ✗     | ✗   | ✓          |
| Audit Logs          | ✗    | ✗     | ✗   | ✓          |

### 4. **Gateway Locking**

Automatically locks the gateway when:

- License is expired or inactive
- Free trial has ended
- License status is invalid

When locked, all requests (except bypass paths) return HTTP 403 with details.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Gateway Server                        │
├─────────────────────────────────────────────────────────┤
│                                                           │
│  ┌──────────────────────────────────────────────────┐   │
│  │           License Enforcer (Middleware)          │   │
│  │  - Fetches license from License Service          │   │
│  │  - Syncs once per day (configurable)             │   │
│  │  - Caches state in memory                        │   │
│  └────────────────┬─────────────────────────────────┘   │
│                   │                                       │
│                   ▼                                       │
│  ┌──────────────────────────────────────────────────┐   │
│  │           License Monitor Service                │   │
│  │  - Monitors license state (hourly checks)        │   │
│  │  - Tracks usage (RPS, RPM, RPH, total)          │   │
│  │  - Enforces feature gates                        │   │
│  │  - Manages gateway lock state                    │   │
│  └────────────────┬─────────────────────────────────┘   │
│                   │                                       │
│                   ▼                                       │
│  ┌──────────────────────────────────────────────────┐   │
│  │         License Monitoring Middleware            │   │
│  │  - Records request metrics                       │   │
│  │  - Checks usage limits per request               │   │
│  │  - Blocks requests if locked                     │   │
│  └──────────────────────────────────────────────────┘   │
│                                                           │
└─────────────────────────────────────────────────────────┘
```

## Usage

### Initialization

The license monitor is automatically initialized and started during gateway startup:

```go
// In cmd/serve/start_api.go
licMonitor := licensemonitor.NewMonitor(sharedLE, licensemonitor.Config{
    CheckInterval: 1 * time.Hour,        // Check license state every hour
    WarnBefore:    7 * 24 * time.Hour,   // Warn 7 days before expiry
})
licMonitor.Start(ctx)
```

### Middleware Integration

Apply the monitoring middleware to track usage:

```go
// Apply to specific routes or globally
router.Use(licensemonitor.WithLicenseMonitoring(monitor, bypassPaths))
```

Apply feature gates to specific endpoints:

```go
// Require Pro tier for advanced analytics
analyticsHandler := licensemonitor.WithFeatureGate(monitor, "advanced_analytics")(
    http.HandlerFunc(handleAnalytics),
)
```

### API Endpoints

#### Get License Status

```bash
GET /api/v1/license/status
```

Response:

```json
{
  "license": {
    "active": true,
    "tier": "pro",
    "status": "active",
    "is_paid": true,
    "expires_at": "2025-12-31T23:59:59Z",
    "fetched_at": "2025-10-20T10:00:00Z",
    "usage_limits": [
      { "type": "RPM", "limit": 6000 },
      { "type": "REQUESTS", "limit": 1000000 }
    ]
  },
  "usage": {
    "rpm": 45,
    "rps": 2,
    "rph": 2340,
    "total_requests": 156789,
    "last_reset": "2025-10-01T00:00:00Z",
    "requests_in_min": 45,
    "requests_in_sec": 2,
    "requests_in_hour": 2340
  },
  "gateway": {
    "locked": false,
    "features": [
      {
        "name": "core_api",
        "enabled": true,
        "required_tier": "free"
      },
      {
        "name": "advanced_analytics",
        "enabled": true,
        "required_tier": "pro"
      },
      {
        "name": "custom_integrations",
        "enabled": true,
        "required_tier": "pro"
      },
      {
        "name": "sso",
        "enabled": false,
        "required_tier": "enterprise",
        "locked_reason": "Upgrade to Enterprise to access SSO"
      }
    ]
  }
}
```

#### Refresh License State

```bash
POST /api/v1/license/refresh
```

Forces an immediate license state refresh and returns updated status.

### Programmatic Usage

#### Check if Gateway is Locked

```go
locked, reason := monitor.IsLocked()
if locked {
    log.Printf("Gateway is locked: %s", reason)
}
```

#### Check Feature Availability

```go
enabled, reason := monitor.IsFeatureEnabled("advanced_analytics")
if !enabled {
    log.Printf("Feature not available: %s", reason)
}
```

#### Get Current License State

```go
state := monitor.GetLicenseState()
if state != nil {
    log.Printf("Current tier: %s, expires: %v", state.Tier, state.ExpiresAt)
}
```

#### Get Usage Statistics

```go
usage := monitor.GetUsageStats()
log.Printf("Current RPM: %d, Total requests: %d", usage.RPM, usage.TotalRequests)
```

#### Subscribe to License Changes

```go
monitor.Subscribe(func(state LicenseState) {
    log.Printf("License state changed: tier=%s, active=%v", state.Tier, state.Active)
})
```

## Configuration

The license monitor uses configuration from the License Enforcer:

```yaml
# cmd/config/services/defaults/services.yaml
services:
  security:
    license_enforcement:
      enabled: true
      dry_run: false
      cache_ttl: "24h" # How often to sync with license service
```

## Monitoring & Observability

The license monitor emits structured logs for:

- License sync operations
- Usage limit violations
- Gateway lock/unlock events
- Expiry warnings
- Feature access attempts

Example logs:

```
[INFO]  license_monitor: starting license monitoring service
[INFO]  license_monitor: successfully synced license state (tier: pro, status: active, active: true)
[WARN]  license_monitor: license expires in 5 days (2025-10-25)
[ERROR] license_monitor: GATEWAY LOCKED - license expired on 2025-10-20T00:00:00Z
[WARN]  license_monitor: usage limit exceeded: RPM limit is 60, current usage is 61
```

## Error Responses

### Usage Limit Exceeded

```json
HTTP 429 Too Many Requests
{
  "error": {
    "type": "license_error",
    "message": "Usage limit exceeded",
    "details": "usage limit exceeded: RPM limit is 60, current usage is 61",
    "code": 429
  }
}
```

### Gateway Locked

```json
HTTP 403 Forbidden
{
  "error": {
    "type": "license_error",
    "message": "Gateway is locked",
    "details": "license expired on 2025-10-20T00:00:00Z",
    "code": 403
  }
}
```

### Feature Not Available

```json
HTTP 403 Forbidden
{
  "error": {
    "type": "license_error",
    "message": "Feature 'advanced_analytics' not available",
    "details": "Upgrade to Pro or Enterprise to access advanced analytics",
    "code": 403
  }
}
```

## Implementation Details

### Usage Counter Reset Logic

- **Second counters**: Reset every second
- **Minute counters**: Reset every minute
- **Hour counters**: Reset every hour
- **Monthly counters**: Reset on the 1st day of each month at midnight

### License State Checks

- Performed every hour (configurable via `CheckInterval`)
- Immediate check on server startup
- Can be triggered manually via API (`POST /api/v1/license/refresh`)

### Thread Safety

All operations are thread-safe using `sync.RWMutex` for concurrent access.

## Future Enhancements

Potential improvements:

1. Persist usage statistics to database for historical analysis
2. Add alerting/webhooks for license events
3. Implement soft limits with grace periods
4. Add per-API-key usage tracking
5. Support custom usage limits per instance
6. Implement credit-based usage tracking
7. Add usage forecasting and quota recommendations
