package httpclient

import (
	"context"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// ProviderEndpoint represents a provider's endpoint for pre-warming.
type ProviderEndpoint struct {
	// Name is the provider name (for logging)
	Name string
	// BaseURL is the provider's base URL
	BaseURL string
	// HealthPath is the path to use for health checks (e.g., "/health", "/v1/models")
	// If empty, a HEAD request to BaseURL is used
	HealthPath string
}

// PrewarmConfig configures connection pre-warming behavior.
type PrewarmConfig struct {
	// ConnectionsPerProvider is how many connections to pre-warm per provider
	ConnectionsPerProvider int
	// Timeout is the timeout for each pre-warm request
	Timeout time.Duration
	// RetryInterval is how long to wait before retrying failed pre-warms
	RetryInterval time.Duration
	// MaxRetries is the maximum number of retries per provider
	MaxRetries int
}

// DefaultPrewarmConfig returns sensible defaults for pre-warming.
func DefaultPrewarmConfig() PrewarmConfig {
	return PrewarmConfig{
		ConnectionsPerProvider: 10,
		Timeout:                5 * time.Second,
		RetryInterval:          30 * time.Second,
		MaxRetries:             3,
	}
}

// PrewarmResult contains the results of a pre-warm operation.
type PrewarmResult struct {
	Provider          string
	SuccessCount      int
	FailCount         int
	Duration          time.Duration
	LastError         error
	ConnectionsWarmed int
}

// PrewarmStats tracks pre-warming statistics.
type PrewarmStats struct {
	TotalProviders    int
	SuccessfulWarms   atomic.Int64
	FailedWarms       atomic.Int64
	LastPrewarmTime   atomic.Value // time.Time
	PrewarmInProgress atomic.Bool
}

var globalPrewarmStats = &PrewarmStats{}

// PrewarmConnections pre-warms HTTP connections to the specified providers.
// This runs in the background and does not block.
// It makes lightweight requests to each provider to establish connections
// that will be reused by subsequent requests.
func PrewarmConnections(ctx context.Context, providers []ProviderEndpoint) {
	PrewarmConnectionsWithConfig(ctx, providers, DefaultPrewarmConfig())
}

// PrewarmConnectionsWithConfig pre-warms connections with custom configuration.
func PrewarmConnectionsWithConfig(ctx context.Context, providers []ProviderEndpoint, cfg PrewarmConfig) {
	if len(providers) == 0 {
		return
	}

	// Prevent concurrent pre-warm operations
	if !globalPrewarmStats.PrewarmInProgress.CompareAndSwap(false, true) {
		return
	}

	go func() {
		defer globalPrewarmStats.PrewarmInProgress.Store(false)
		defer func() {
			globalPrewarmStats.LastPrewarmTime.Store(time.Now())
		}()

		globalPrewarmStats.TotalProviders = len(providers)

		var wg sync.WaitGroup
		results := make(chan PrewarmResult, len(providers))

		for _, provider := range providers {
			wg.Add(1)
			go func(p ProviderEndpoint) {
				defer wg.Done()
				result := prewarmProvider(ctx, p, cfg)
				results <- result

				if result.FailCount == 0 {
					globalPrewarmStats.SuccessfulWarms.Add(1)
				} else {
					globalPrewarmStats.FailedWarms.Add(1)
				}
			}(provider)
		}

		// Wait for all pre-warms to complete
		go func() {
			wg.Wait()
			close(results)
		}()

		// Drain results (could be logged or reported)
		for range results {
			// Results are tracked via atomic counters
		}
	}()
}

// prewarmProvider warms connections to a single provider.
func prewarmProvider(ctx context.Context, provider ProviderEndpoint, cfg PrewarmConfig) PrewarmResult {
	start := time.Now()
	result := PrewarmResult{
		Provider: provider.Name,
	}

	url := provider.BaseURL
	if provider.HealthPath != "" {
		url = provider.BaseURL + provider.HealthPath
	}

	client := Default()

	// Pre-warm connections in parallel
	var wg sync.WaitGroup
	var successCount, failCount atomic.Int32

	for i := 0; i < cfg.ConnectionsPerProvider; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			reqCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
			defer cancel()

			// Use HEAD request to minimize data transfer
			req, err := http.NewRequestWithContext(reqCtx, http.MethodHead, url, nil)
			if err != nil {
				failCount.Add(1)
				result.LastError = err
				return
			}

			// Add a custom header to identify pre-warm requests
			req.Header.Set("X-Prewarm", "true")

			resp, err := client.Do(req)
			if err != nil {
				failCount.Add(1)
				result.LastError = err
				return
			}

			// Drain and close body to ensure connection is returned to pool
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			// Any response (even 4xx/5xx) means the connection was established
			successCount.Add(1)
		}()
	}

	wg.Wait()

	result.SuccessCount = int(successCount.Load())
	result.FailCount = int(failCount.Load())
	result.Duration = time.Since(start)
	result.ConnectionsWarmed = result.SuccessCount

	return result
}

// PrewarmConnectionsSync pre-warms connections synchronously.
// This blocks until all pre-warming is complete.
// Use this during startup if you need to ensure connections are ready.
func PrewarmConnectionsSync(ctx context.Context, providers []ProviderEndpoint, cfg PrewarmConfig) []PrewarmResult {
	if len(providers) == 0 {
		return nil
	}

	results := make([]PrewarmResult, len(providers))
	var wg sync.WaitGroup

	for i, provider := range providers {
		wg.Add(1)
		go func(idx int, p ProviderEndpoint) {
			defer wg.Done()
			results[idx] = prewarmProvider(ctx, p, cfg)
		}(i, provider)
	}

	wg.Wait()
	return results
}

// GetPrewarmStats returns the current pre-warming statistics.
func GetPrewarmStats() (totalProviders int, successful, failed int64, lastPrewarm time.Time, inProgress bool) {
	totalProviders = globalPrewarmStats.TotalProviders
	successful = globalPrewarmStats.SuccessfulWarms.Load()
	failed = globalPrewarmStats.FailedWarms.Load()
	if v := globalPrewarmStats.LastPrewarmTime.Load(); v != nil {
		lastPrewarm = v.(time.Time)
	}
	inProgress = globalPrewarmStats.PrewarmInProgress.Load()
	return
}

// ResetPrewarmStats resets the pre-warming statistics.
func ResetPrewarmStats() {
	globalPrewarmStats.TotalProviders = 0
	globalPrewarmStats.SuccessfulWarms.Store(0)
	globalPrewarmStats.FailedWarms.Store(0)
	globalPrewarmStats.LastPrewarmTime.Store(time.Time{})
}

// SchedulePeriodicPrewarm schedules periodic connection pre-warming.
// This helps maintain warm connections during low-traffic periods.
// Returns a channel that can be closed to stop the scheduler.
func SchedulePeriodicPrewarm(ctx context.Context, providers []ProviderEndpoint, cfg PrewarmConfig, interval time.Duration) chan<- struct{} {
	stop := make(chan struct{})

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				PrewarmConnectionsWithConfig(ctx, providers, cfg)
			case <-stop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	return stop
}

