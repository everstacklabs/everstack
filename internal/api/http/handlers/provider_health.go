package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/everstacklabs/everstack/internal/providers/health"
)

// ProviderHealthHandler returns health status for all providers.
// GET /health/providers
func ProviderHealthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get health data from the global tracker
	response := health.GetAllProviderHealth()

	// Set response headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	// Determine HTTP status based on overall health
	statusCode := http.StatusOK
	if response.Unhealthy > 0 {
		statusCode = http.StatusServiceUnavailable
	} else if response.Degraded > 0 {
		statusCode = http.StatusOK // Still return 200 for degraded, include status in body
	}

	w.WriteHeader(statusCode)

	// Write response
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

// ProviderHealthByNameHandler returns health status for a specific provider.
// GET /health/providers/{name}
func ProviderHealthByNameHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract provider name from path
	// Expected path: /health/providers/openai
	path := r.URL.Path
	// Remove prefix to get provider name
	const prefix = "/health/providers/"
	if len(path) <= len(prefix) {
		// Return all providers if no name specified
		ProviderHealthHandler(w, r)
		return
	}

	providerName := path[len(prefix):]
	if providerName == "" {
		ProviderHealthHandler(w, r)
		return
	}

	// Get health for specific provider
	providerHealth := health.GetProviderHealth(providerName)

	// Set response headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	// Determine HTTP status
	statusCode := http.StatusOK
	if providerHealth.Status == health.StatusUnhealthy {
		statusCode = http.StatusServiceUnavailable
	}

	w.WriteHeader(statusCode)

	// Write response
	if err := json.NewEncoder(w).Encode(providerHealth); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}
