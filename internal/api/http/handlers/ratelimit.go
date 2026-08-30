package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/everstacklabs/everstack/internal/providers/ratelimit"
)

// RateLimitStatusHandler returns current rate limit status for all providers
func RateLimitStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status := make(map[string]*ratelimit.RateLimitInfo)
	providers := ratelimit.GlobalMonitor.GetKnownProviders()

	for _, provider := range providers {
		if info := ratelimit.GlobalMonitor.GetStatus(provider); info != nil {
			status[provider] = info
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"timestamp": time.Now(),
		"providers": status,
	})
}

// RateLimitSubscribeHandler provides SSE stream of rate limit updates
func RateLimitSubscribeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Create a channel for rate limit updates
	updates := make(chan ratelimit.RateLimitInfo, 10)

	// Subscribe to rate limit updates and defer cleanup
	unsubscribe := ratelimit.GlobalMonitor.Subscribe(func(info ratelimit.RateLimitInfo) {
		select {
		case updates <- info:
		default:
			// Drop update if channel is full
		}
	})
	defer unsubscribe()

	// Send initial status
	providers := ratelimit.GlobalMonitor.GetKnownProviders()
	for _, provider := range providers {
		if info := ratelimit.GlobalMonitor.GetStatus(provider); info != nil {
			data, _ := json.Marshal(info)
			w.Write([]byte("data: "))
			w.Write(data)
			w.Write([]byte("\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}

	// Stream updates
	for {
		select {
		case info := <-updates:
			data, _ := json.Marshal(info)
			w.Write([]byte("data: "))
			w.Write(data)
			w.Write([]byte("\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}
