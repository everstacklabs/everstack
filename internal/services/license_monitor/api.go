package license_monitor

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/services/trial"
	"github.com/everstacklabs/everstack/pkg/plans"
)

// LicenseStatusResponse represents the API response for license status
type LicenseStatusResponse struct {
	License LicenseInfo `json:"license"`
	Usage   UsageInfo   `json:"usage"`
	Gateway GatewayInfo `json:"gateway"`
}

// LicenseInfo contains license details
type LicenseInfo struct {
	Active                bool       `json:"active"`
	Tier                  string     `json:"tier"`
	Status                string     `json:"status"`
	IsPaid                bool       `json:"is_paid"`
	ExpiresAt             *time.Time `json:"expires_at,omitempty"`
	TrialExpires          *time.Time `json:"trial_expires,omitempty"`
	FetchedAt             time.Time  `json:"fetched_at"`
	SandboxBillingEnabled bool       `json:"sandbox_billing_enabled"`
	UsageLimits           []struct {
		Type  string `json:"type"`
		Limit int64  `json:"limit"`
	} `json:"usage_limits"`
}

// UsageInfo contains current usage statistics
type UsageInfo struct {
	// Request rate metrics
	RPM            int64     `json:"rpm"`              // Peak requests per minute
	RPS            int64     `json:"rps"`              // Peak requests per second
	RPH            int64     `json:"rph"`              // Peak requests per hour
	TotalRequests  int64     `json:"total_requests"`   // Total requests this period
	LastReset      time.Time `json:"last_reset"`       // Last time monthly counter was reset
	RequestsInMin  int64     `json:"requests_in_min"`  // Requests in current minute
	RequestsInSec  int64     `json:"requests_in_sec"`  // Requests in current second
	RequestsInHour int64     `json:"requests_in_hour"` // Requests in current hour

	// Token metrics
	TotalInputTokens  int64 `json:"total_input_tokens"`  // Total input tokens processed
	TotalOutputTokens int64 `json:"total_output_tokens"` // Total output tokens generated
	TotalTokens       int64 `json:"total_tokens"`        // Total tokens (input + output)

	// Cost metrics (in USD)
	EstimatedCostUSD float64 `json:"estimated_cost_usd"` // Estimated total cost
	CacheSavingsUSD  float64 `json:"cache_savings_usd"`  // Cost savings from cache hits

	// Cache performance metrics
	CacheHits   int64 `json:"cache_hits"`   // Number of cache hits
	CacheMisses int64 `json:"cache_misses"` // Number of cache misses
}

// GatewayInfo contains gateway status
type GatewayInfo struct {
	Locked       bool   `json:"locked"`
	LockReason   string `json:"lock_reason,omitempty"`
	FeaturesInfo []struct {
		Name         string `json:"name"`
		Enabled      bool   `json:"enabled"`
		RequiredTier string `json:"required_tier,omitempty"`
		LockedReason string `json:"locked_reason,omitempty"`
	} `json:"features"`
}

// HandleLicenseStatus returns the current license and usage status as JSON
func HandleLicenseStatus(monitor *Monitor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		state := monitor.GetLicenseState()
		usage := monitor.GetUsageStats()
		locked, lockReason := monitor.IsLocked()

		response := LicenseStatusResponse{
			Usage: UsageInfo{
				// Request rate metrics
				RPM:            usage.RPM,
				RPS:            usage.RPS,
				RPH:            usage.RPH,
				TotalRequests:  usage.TotalRequests,
				LastReset:      usage.LastReset,
				RequestsInMin:  usage.RequestsInMin,
				RequestsInSec:  usage.RequestsInSec,
				RequestsInHour: usage.RequestsInHour,
				// Token metrics
				TotalInputTokens:  usage.TotalInputTokens,
				TotalOutputTokens: usage.TotalOutputTokens,
				TotalTokens:       usage.TotalTokens,
				// Cost metrics
				EstimatedCostUSD: usage.EstimatedCostUSD,
				CacheSavingsUSD:  usage.CacheSavingsUSD,
				// Cache performance metrics
				CacheHits:   usage.CacheHits,
				CacheMisses: usage.CacheMisses,
			},
			Gateway: GatewayInfo{
				Locked:     locked,
				LockReason: lockReason,
			},
		}

		// Add license info if available
		if state != nil {
			response.License = LicenseInfo{
				Active:                state.Active,
				Tier:                  state.Tier,
				Status:                state.Status,
				IsPaid:                state.IsPaid,
				ExpiresAt:             state.ExpiresAt,
				TrialExpires:          state.TrialExpires,
				FetchedAt:             state.FetchedAt,
				SandboxBillingEnabled: state.SandboxBillingEnabled,
			}

			// Convert usage limits
			response.License.UsageLimits = make([]struct {
				Type  string `json:"type"`
				Limit int64  `json:"limit"`
			}, len(state.UsageLimits))

			for i, limit := range state.UsageLimits {
				response.License.UsageLimits[i].Type = string(limit.Type)
				response.License.UsageLimits[i].Limit = limit.Limit
			}
		}

		// Add feature info
		monitor.mu.RLock()
		features := make([]struct {
			Name         string `json:"name"`
			Enabled      bool   `json:"enabled"`
			RequiredTier string `json:"required_tier,omitempty"`
			LockedReason string `json:"locked_reason,omitempty"`
		}, 0, len(monitor.features))

		for name, featureState := range monitor.features {
			features = append(features, struct {
				Name         string `json:"name"`
				Enabled      bool   `json:"enabled"`
				RequiredTier string `json:"required_tier,omitempty"`
				LockedReason string `json:"locked_reason,omitempty"`
			}{
				Name:         name,
				Enabled:      featureState.Enabled,
				RequiredTier: featureState.RequiredTier,
				LockedReason: featureState.LockedReason,
			})
		}
		monitor.mu.RUnlock()

		response.Gateway.FeaturesInfo = features

		// Write response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}
}

// HandleRefreshLicense forces a refresh of the license state
func HandleRefreshLicense(monitor *Monitor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Force a license state check
		monitor.checkLicenseState()

		// Return updated status
		HandleLicenseStatus(monitor)(w, r)
	}
}

// HandleGetPlans returns available license plans from local configuration file
func HandleGetPlans(plansConfigPath string) http.HandlerFunc {
	// Load plans config once at initialization
	plansConfig, err := plans.Load(plansConfigPath)
	if err != nil {
		// Return error handler if config can't be loaded
		return func(w http.ResponseWriter, r *http.Request) {
			logger.WithError(err).Error("Failed to load plans configuration")
			http.Error(w, "Failed to load plans configuration", http.StatusInternalServerError)
		}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(plansConfig); err != nil {
			http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		}
	}
}

// HandleTrialStatus returns the current trial mode status
func HandleTrialStatus(trialManager *trial.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")

		if trialManager == nil {
			response := map[string]interface{}{
				"mode":    "disabled",
				"active":  false,
				"message": "Trial mode is not available",
			}
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		status := trialManager.GetStatus()
		status["mode"] = "trial"

		// Add mode indication
		if trialManager.IsActive() && !trialManager.IsExpired() {
			status["message"] = "Gateway is running in trial mode"
		} else if trialManager.IsExpired() {
			status["message"] = "Trial period has expired"
		}

		_ = json.NewEncoder(w).Encode(status)
	}
}
