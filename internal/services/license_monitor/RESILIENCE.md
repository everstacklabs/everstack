# License Monitor Resilience

## Overview

The License Monitor is designed to be **highly resilient** and continue serving requests even when the License Service is temporarily unavailable.

## Resilience Strategy

### Core Principle

**"Fail open with cached data, not fail closed"**

The gateway should remain operational using cached license information when the license service is down, rather than blocking all traffic.

### Behavior

#### 1. **Normal Operation**

```
License Service UP
    ↓
Sync every 24 hours
    ↓
Update cache
    ↓
Serve requests ✅
```

#### 2. **License Service Down**

```
License Service DOWN ❌
    ↓
Sync fails
    ↓
Keep existing cache 💾
    ↓
Serve requests ✅ (using cached license)
    ↓
Log warning
```

#### 3. **Initial Startup (No Cache)**

```
First startup
    ↓
No cached state
    ↓
License Service DOWN ❌
    ↓
Lock gateway 🔒 (no license info)
    ↓
Retry on next sync interval
```

## Implementation Details

### LicenseEnforcer (Middleware)

The `refresh()` method in `license_enforcer.go`:

```go
func (l *LicenseEnforcer) refresh() error {
    st := l.fetch()
    l.mu.Lock()
    defer l.mu.Unlock()

    if st != nil {
        // Successfully fetched new state - update cache
        st.FetchedAt = time.Now().UTC()
        l.cached = st
        logger.Infof("license_enforcer: successfully synced license state")
    } else {
        // Failed to fetch - keep existing cache for resilience
        if l.cached != nil {
            logger.Warnf("license_enforcer: failed to fetch fresh license state, continuing with cached state from %s",
                l.cached.FetchedAt.Format(time.RFC3339))
        } else {
            logger.Error("license_enforcer: failed to fetch license state and no cached state available")
        }
        // DON'T set l.cached = nil - preserve existing cache!
    }
    return nil
}
```

**Key Points:**

- ✅ **Preserves cache** when fetch fails
- ✅ **Logs warnings** for monitoring
- ✅ **Continues serving** with stale data
- ❌ **Does NOT block** requests

### LicenseMonitor (Usage Tracking)

The `checkLicenseState()` method in `license_monitor/monitor.go`:

```go
func (m *Monitor) checkLicenseState() {
    cachedState := m.enforcer.GetCached()
    if cachedState == nil {
        // Check if we have existing state
        m.mu.RLock()
        hasExisting := m.licenseState != nil
        m.mu.RUnlock()

        if !hasExisting {
            // First time and no state - lock gateway
            m.setLocked(true, "No license information available")
        } else {
            // Service down but we have cache - continue
            logger.Warn("license service unavailable, continuing with cached state")
        }
        return
    }

    // Update state and validate based on expiry, not service availability
    // ...
}
```

**Key Points:**

- ✅ **Differentiates** between first startup vs. service down
- ✅ **Validates expiry** locally (doesn't need service)
- ✅ **Continues operation** with cached data
- ❌ **Only locks** if no cache exists at all

## Cache Lifetime

### When Cache is Updated

1. ✅ Server startup (initial sync)
2. ✅ Every 24 hours (scheduled sync)
3. ✅ Manual refresh via API (`POST /api/v1/license/refresh`)
4. ✅ Server restart

### When Cache is Used

1. ✅ License service temporarily down
2. ✅ Network issues
3. ✅ License service timeout
4. ✅ Between scheduled syncs (every request)

### When Gateway Locks

1. 🔒 **First startup** AND license service down (no cache)
2. 🔒 **License expired** (even with valid cache)
3. 🔒 **Trial ended** (even with valid cache)
4. 🔒 **License status** = inactive/suspended/cancelled

**NOT locked for:**

- ❌ License service temporary outage (uses cache)
- ❌ Network issues (uses cache)
- ❌ Service timeout (uses cache)

## Validation Logic

### Local Validation (No Service Required)

These checks happen using cached data:

```go
// Check expiry date
if state.ExpiresAt != nil && state.ExpiresAt.Before(now) {
    return fmt.Errorf("license expired")
}

// Check trial expiry
if !state.IsPaid && state.TrialExpires != nil && state.TrialExpires.Before(now) {
    return fmt.Errorf("free trial expired")
}

// Check status
if !state.Active {
    return fmt.Errorf("license is not active")
}
```

**No network calls required!** All validation is local.

### Remote Sync (Service Required)

Only happens during scheduled syncs:

- Fetch latest license state
- Update tier/limits/status
- Refresh expiry dates

## Monitoring & Alerting

### Log Messages

#### Success (Normal)

```
[INFO] license_enforcer: successfully synced license state (tier: pro, status: active, active: true)
```

#### Service Down (Resilient)

```
[WARN] license_enforcer: failed to fetch fresh license state, continuing with cached state from 2025-10-20T10:00:00Z (tier: pro, active: true)
[WARN] license_monitor: license service unavailable, continuing with cached state
```

#### Critical (Gateway Locked)

```
[ERROR] license_enforcer: failed to fetch license state and no cached state available
[ERROR] license_monitor: GATEWAY LOCKED - No license information available
```

### Metrics to Monitor

1. **`license_sync_failures`** - Count of failed sync attempts
2. **`license_cache_age_seconds`** - Age of cached license data
3. **`license_service_up`** - Is license service reachable
4. **`gateway_locked`** - Is gateway currently locked

### Alerts

#### Warning

- License service down for > 1 hour
- Cache age > 48 hours
- License expires in < 7 days

#### Critical

- Gateway locked
- License service down for > 24 hours
- License expired
- Cache age > 7 days

## Scenarios

### Scenario 1: License Service Outage

```
Timeline:
10:00 - License synced successfully
12:00 - License service goes down
14:00 - Scheduled sync fails, uses cache from 10:00 ✅
16:00 - Scheduled sync fails, uses cache from 10:00 ✅
18:00 - License service back up
18:00 - Scheduled sync succeeds, cache updated ✅

Result: Gateway operational entire time
```

### Scenario 2: Extended Outage

```
Timeline:
Day 1 10:00 - License synced successfully
Day 1 12:00 - License service goes down
Day 2-7     - All syncs fail, uses cache ✅
Day 8       - License expires
Day 8       - Gateway locks 🔒 (expiry validation fails)

Result: Gateway works until actual license expiry
```

### Scenario 3: First Startup + Service Down

```
Timeline:
10:00 - Gateway starts, no cache
10:00 - License service down
10:00 - Gateway locks 🔒 (no license info)
10:01 - Retry sync
10:01 - Still down, gateway locked 🔒
10:30 - License service up
10:30 - Sync succeeds
10:30 - Gateway unlocks ✅

Result: Gateway locked until first successful sync
```

## Best Practices

### For Operators

1. **Monitor cache age**
   - Alert if > 48 hours
   - Investigate if > 7 days

2. **Check license service health**
   - Set up uptime monitoring
   - Alert on extended outages

3. **Plan for outages**
   - Gateway will work for days/weeks if license valid
   - No immediate panic if service goes down

4. **Test resilience**
   ```bash
   # Stop license service
   # Gateway should continue working
   # Check logs for warnings
   ```

### For Developers

1. **Always preserve cache** on fetch failure
2. **Validate locally** when possible (expiry, status)
3. **Log clearly** for operational visibility
4. **Test failure modes** in integration tests

## Testing

### Manual Testing

#### Test 1: Service Outage

```bash
# 1. Start gateway with license service running
# 2. Verify license syncs successfully
# 3. Stop license service
# 4. Wait for next sync interval
# 5. Check logs: "continuing with cached state"
# 6. Verify gateway still serving requests ✅
```

#### Test 2: First Startup Without Service

```bash
# 1. Stop license service
# 2. Start gateway (fresh, no cache)
# 3. Check logs: "failed to fetch license state and no cached state available"
# 4. Verify gateway is locked 🔒
# 5. Start license service
# 6. Wait for next sync
# 7. Verify gateway unlocks ✅
```

#### Test 3: Manual Refresh During Outage

```bash
# 1. Stop license service
# 2. Call POST /api/v1/license/refresh
# 3. Should fail but preserve cache
# 4. Verify gateway still works ✅
```

### Integration Tests

```go
func TestLicenseResilienceOnServiceDown(t *testing.T) {
    // 1. Start with valid license
    // 2. Populate cache
    // 3. Kill license service
    // 4. Trigger refresh
    // 5. Assert cache preserved
    // 6. Assert gateway not locked
}

func TestGatewayLocksOnFirstStartupWithoutService(t *testing.T) {
    // 1. No cache exists
    // 2. License service down
    // 3. Start gateway
    // 4. Assert gateway locked
}

func TestLocalValidationWithoutService(t *testing.T) {
    // 1. Cache expired license
    // 2. License service down
    // 3. Check validation
    // 4. Assert gateway locked (expired)
}
```

## Summary

The license system is designed for **maximum uptime and resilience**:

- ✅ Works for days/weeks if license service down (as long as license not expired)
- ✅ Validates expiry locally without network calls
- ✅ Clear logging for operational visibility
- ✅ Preserves cached state on fetch failures
- ✅ Only locks gateway on actual license issues (expiry, invalid), not service issues

**Bottom Line:** If your license is valid and cached, the gateway keeps working even if the license service is completely down. The only time it locks is if:

1. First startup AND no service (no cache to use)
2. License actually expires/invalidated (local validation)
