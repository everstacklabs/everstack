package license_monitor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// WithLicenseMonitoring is middleware that checks license state and usage limits before processing requests
func WithLicenseMonitoring(monitor *Monitor, bypassPaths []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip monitoring for bypass paths
			for _, path := range bypassPaths {
				if strings.HasPrefix(r.URL.Path, path) {
					next.ServeHTTP(w, r)
					return
				}
			}

			// Check if gateway is locked
			if locked, reason := monitor.IsLocked(); locked {
				logger.Debugf("license_monitor: request blocked - gateway locked: %s", reason)
				writeError(w, http.StatusForbidden, "Gateway is locked", reason)
				return
			}

			// Check if spending is blocked (limit exceeded with block action)
			if blocked, reason := monitor.IsSpendBlocked(); blocked {
				logger.Warnf("license_monitor: request blocked - spend limit exceeded: %s", reason)
				writeSpendLimitError(w, reason)
				return
			}

			// NOTE: We intentionally do NOT call RecordRequest() here.
			// The gateway processor calls RecordRequestWithMetrics() after the request
			// completes with actual token/cost data. Calling both would double-count.

			// Continue to next handler
			next.ServeHTTP(w, r)
		})
	}
}

// WithFeatureGate is middleware that checks if a specific feature is enabled
func WithFeatureGate(monitor *Monitor, feature string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			enabled, reason := monitor.IsFeatureEnabled(feature)
			if !enabled {
				logger.Warnf("license_monitor: feature '%s' not available: %s", feature, reason)
				writeError(w, http.StatusForbidden, fmt.Sprintf("Feature '%s' not available", feature), reason)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// WithSpendLimitCheck is middleware that pre-checks spend limits before processing requests
// This is used for requests where we can estimate the cost upfront
func WithSpendLimitCheck(monitor *Monitor, estimateCost func(*http.Request) float64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Estimate the cost of this request
			estimatedCost := float64(0)
			if estimateCost != nil {
				estimatedCost = estimateCost(r)
			}

			// Check if the request would exceed spend limits
			allowed, message := monitor.CheckSpendLimitBeforeRequest(estimatedCost)
			if !allowed {
				logger.Debugf("license_monitor: request blocked by spend limit: %s", message)
				writeSpendLimitError(w, message)
				return
			}

			// Continue to next handler
			next.ServeHTTP(w, r)
		})
	}
}

// writeSpendLimitError writes a spend limit exceeded error response
func writeSpendLimitError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPaymentRequired) // 402 Payment Required

	payload := map[string]interface{}{
		"error": map[string]interface{}{
			"type":    "spend_limit_exceeded",
			"message": "Spend limit exceeded",
			"details": message,
			"code":    402,
		},
	}

	_ = json.NewEncoder(w).Encode(payload)
}

// writeError writes a consistent JSON error response
func writeError(w http.ResponseWriter, status int, message, details string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	payload := map[string]interface{}{
		"error": map[string]interface{}{
			"type":    "license_error",
			"message": message,
			"details": details,
			"code":    status,
		},
	}

	_ = json.NewEncoder(w).Encode(payload)
}
