# License Monitoring UI

## Overview

The License Monitoring UI provides a comprehensive dashboard for viewing license status, usage statistics, and feature availability in real-time.

## Features

### 🎯 **Real-Time Monitoring**

- **Auto-refresh**: Polls API every 30 seconds for fresh data
- **Manual refresh**: Button to force immediate sync with license service
- **Focus-aware**: Automatically refetches when browser tab regains focus
- **Smart caching**: Uses TanStack Query for optimized data fetching

### 📊 **Usage Statistics**

Visual progress bars showing:

- **RPM** (Requests Per Minute) - with current usage vs. limit
- **RPS** (Requests Per Second)
- **RPH** (Requests Per Hour)
- **Total Requests** - Monthly counter with reset date

Color-coded indicators:

- 🟢 **Green**: < 80% usage (healthy)
- 🟡 **Yellow**: 80-99% usage (approaching limit)
- 🔴 **Red**: ≥ 100% usage (limit exceeded)

### 🚨 **Intelligent Alerts**

Automatic warnings for:

- Gateway locked status (red alert)
- License expiring soon (< 7 days, yellow warning)
- Usage limits approaching (> 80%)
- Usage limits exceeded

### 🎫 **License Information**

At-a-glance display of:

- Current tier (Free, Basic, Pro, Enterprise)
- License status (Active, Expired, etc.)
- Expiration date with countdown
- Gateway lock status
- Last sync timestamp

### ✨ **Feature Availability**

Clear visualization of:

- Which features are enabled for your tier
- Features locked behind higher tiers
- Upgrade requirements for locked features
- Color-coded feature cards (green = enabled, gray = locked)

## Implementation

### Files Created

1. **`apps/admin/src/server/license.ts`** (updated)
   - Added `getLicenseStatus()` - Fetch current status
   - Added `refreshLicenseStatus()` - Force sync with license service
   - TypeScript interfaces for API responses

2. **`apps/admin/src/hooks/license/use-license-status.ts`**
   - `useLicenseStatus()` - Main hook with polling
   - `useRefreshLicense()` - Manual refresh mutation
   - `useLicenseInfo()` - Computed properties helper
   - `useLicenseSummary()` - Human-readable summary

3. **`apps/admin/src/components/license/license-status-page.tsx`**
   - Full dashboard component
   - Responsive grid layout
   - Interactive charts and progress bars
   - Alert banners

4. **`apps/admin/src/routes/license/index.tsx`**
   - Route definition for `/license` path

5. **`apps/admin/src/components/layout/sidebar/app-sidebar-nav.tsx`** (updated)
   - Added "License" navigation link in sidebar

## Usage

### Access the Dashboard

Navigate to `/license` in the admin panel or click "License" in the sidebar.

### Smart Refresh Strategies

The UI implements intelligent refresh strategies:

#### **1. Automatic Polling (Default)**

```typescript
// Polls every 30 seconds automatically
const { data } = useLicenseStatus({
  enablePolling: true,
  pollingInterval: 30000,
})
```

#### **2. Manual Refresh**

```typescript
const refreshMutation = useRefreshLicense()

// User clicks "Refresh" button
refreshMutation.mutate()
```

#### **3. Focus-Based Refresh**

- Automatically refetches when user returns to the tab
- Configured via TanStack Query's `refetchOnWindowFocus`

#### **4. Custom Intervals**

```typescript
// Poll every 60 seconds instead
const { data } = useLicenseStatus({
  pollingInterval: 60000,
})

// Disable polling
const { data } = useLicenseStatus({
  enablePolling: false,
})
```

### Computed Properties

The `useLicenseInfo()` hook provides useful computed values:

```typescript
const {
  data,
  isLocked, // Gateway locked?
  isExpiringSoon, // < 7 days until expiry?
  isTrialExpiringSoon, // Trial ending soon?
  usagePercentages, // % of each limit used
  isApproachingLimit, // Any limit > 80%?
  isOverLimit, // Any limit >= 100%?
  getFeature, // Get feature by name
  isFeatureEnabled, // Check if feature enabled
} = useLicenseInfo()
```

### Human-Readable Summary

```typescript
const summary = useLicenseSummary()

console.log(summary)
// {
//   tier: 'pro',
//   status: 'active',
//   isPaid: true,
//   isActive: true,
//   isLocked: false,
//   daysUntilExpiry: 45,
//   currentRPM: 234,
//   totalRequests: 15678,
//   ...
// }
```

## UI Components

### Cards

- **License Overview**: Tier, status, expiry, gateway status
- **Usage Statistics**: Real-time metrics with progress bars
- **Feature Availability**: Grid of enabled/locked features
- **License Details**: Full metadata and timestamps

### Alerts

- **Gateway Locked**: Red banner with lock reason
- **Expiry Warning**: Yellow banner with countdown
- **Usage Warning**: Yellow banner when approaching limits

### Interactive Elements

- **Refresh Button**: Force sync with loading spinner
- **Progress Bars**: Color-coded by usage level
- **Feature Cards**: Visual indication of availability
- **Tooltips**: Contextual help text

## Performance

### Caching Strategy

- **Stale Time**: 10 seconds
- **Cache Time**: Infinite (always keep license data)
- **Retry Logic**: 3 attempts with exponential backoff
- **Deduplication**: TanStack Query prevents duplicate requests

### Network Optimization

- Polls every 30s (configurable)
- Pauses polling when window not visible
- Only fetches on focus if data is stale
- Manual refresh bypasses cache

### Bundle Size

- Uses existing dependencies (no new libraries)
- Leverages TanStack Query already in project
- Minimal additional JavaScript

## Accessibility

- Semantic HTML structure
- ARIA labels on interactive elements
- Keyboard navigation support
- Screen reader friendly alerts
- Color + text indicators (not color alone)

## Responsive Design

- Mobile-first approach
- Grid layout adapts to screen size
- Touch-friendly buttons
- Readable on small screens

## Error Handling

### Loading State

```tsx
<Loader loaderText="Loading license information..." />
```

### Error State

```tsx
<Card>
  <CardHeader>
    <CardTitle className="text-destructive">Error Loading License</CardTitle>
    <CardDescription>{error.message}</CardDescription>
  </CardHeader>
  <CardContent>
    <Button onClick={retry}>Try Again</Button>
  </CardContent>
</Card>
```

### No Data State

Gracefully handles missing or incomplete license data.

## Testing

### Manual Testing Checklist

- [ ] Page loads without errors
- [ ] Data refreshes every 30 seconds
- [ ] Manual refresh button works
- [ ] Alerts appear when appropriate
- [ ] Progress bars reflect usage correctly
- [ ] Feature cards show correct state
- [ ] Responsive on mobile
- [ ] Refetches on window focus

### Edge Cases Handled

- License not activated
- License expired
- Usage limits exceeded
- Network errors
- Missing data fields
- Invalid dates

## Future Enhancements

Potential improvements:

1. **Historical Charts**: Show usage trends over time
2. **Usage Forecasting**: Predict when limits will be hit
3. **Export Data**: Download usage reports as CSV/PDF
4. **Notifications**: Browser notifications for critical alerts
5. **Comparison View**: Compare current vs. previous periods
6. **Custom Alerts**: User-defined thresholds for warnings
7. **Upgrade Flow**: In-app upgrade to higher tiers
8. **API Key Usage**: Per-key usage breakdown

## Troubleshooting

### Data Not Refreshing

1. Check browser console for errors
2. Verify API endpoints are accessible
3. Check if polling is enabled
4. Try manual refresh

### Incorrect Usage Stats

1. Backend may be caching - use manual refresh
2. Check if counters have been reset
3. Verify time zones are correct

### Performance Issues

1. Increase polling interval
2. Disable automatic polling
3. Check network tab for request frequency
4. Clear browser cache

## Best Practices

### When to Poll

- **30s**: Real-time monitoring (default)
- **60s**: Reduced server load
- **5min**: Background monitoring
- **Disabled**: Only manual refresh

### When to Force Refresh

- After activation
- After plan changes
- When data seems stale
- After system changes

### Accessibility

- Always provide text alternatives
- Use semantic HTML
- Test with keyboard navigation
- Test with screen readers

## API Integration

The UI consumes these backend endpoints:

```bash
# Get current license status
GET /api/v1/license/status

# Force refresh from license service
POST /api/v1/license/refresh
```

Both return the same response structure:

```typescript
interface LicenseStatusResponse {
  license: {
    active: boolean
    tier: string
    status: string
    is_paid: boolean
    expires_at?: string
    trial_expires?: string
    fetched_at: string
    usage_limits: Array<{
      type: string
      limit: number
    }>
  }
  usage: {
    rpm: number
    rps: number
    rph: number
    total_requests: number
    last_reset: string
    requests_in_min: number
    requests_in_sec: number
    requests_in_hour: number
  }
  gateway: {
    locked: boolean
    lock_reason?: string
    features: Array<{
      name: string
      enabled: boolean
      required_tier?: string
      locked_reason?: string
    }>
  }
}
```

## Summary

The License Monitoring UI provides a production-ready, real-time dashboard for monitoring license status and usage. It implements smart refresh strategies, comprehensive error handling, and an intuitive user interface that works seamlessly across devices.

Key benefits:

- ✅ Real-time monitoring with automatic updates
- ✅ Visual progress indicators for usage tracking
- ✅ Intelligent alerts for critical issues
- ✅ Mobile-responsive design
- ✅ Optimized for performance
- ✅ Fully accessible
- ✅ Easy to extend and customize
