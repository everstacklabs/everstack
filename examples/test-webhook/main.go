// Test webhook server for serverless functions
// Run with: go run main.go
// Listens on http://localhost:9999

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// WebhookPayload is the payload received from the tool loop
type WebhookPayload struct {
	RequestID     string                 `json:"request_id"`
	TenantID      string                 `json:"tenant_id"`
	FunctionID    string                 `json:"function_id"`
	FunctionName  string                 `json:"function_name"`
	Arguments     map[string]interface{} `json:"arguments"`
	CorrelationID string                 `json:"correlation_id"`
	Timestamp     string                 `json:"timestamp"`
}

func main() {
	// Weather endpoint - simulates a weather API
	http.HandleFunc("/weather", handleWeather)

	// Calculator endpoint - simulates a calculator
	http.HandleFunc("/calculator", handleCalculator)

	// Generic echo endpoint - returns whatever it receives
	http.HandleFunc("/echo", handleEcho)

	// Search endpoint - simulates a search API
	http.HandleFunc("/search", handleSearch)

	port := ":9999"
	log.Printf("Test webhook server starting on http://localhost%s", port)
	log.Printf("Available endpoints:")
	log.Printf("  POST /weather    - Get weather for a location")
	log.Printf("  POST /calculator - Perform calculations")
	log.Printf("  POST /echo       - Echo back the payload")
	log.Printf("  POST /search     - Search for information")
	log.Fatal(http.ListenAndServe(port, nil))
}

func handleWeather(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var payload WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	log.Printf("[WEATHER] Received request:")
	log.Printf("  Function: %s", payload.FunctionName)
	log.Printf("  Arguments: %v", payload.Arguments)
	log.Printf("  Correlation ID: %s", payload.CorrelationID)

	// Extract location from arguments
	location := "Unknown"
	if loc, ok := payload.Arguments["location"].(string); ok {
		location = loc
	}

	// Simulate weather data
	response := map[string]interface{}{
		"location":    location,
		"temperature": 22,
		"unit":        "celsius",
		"condition":   "partly cloudy",
		"humidity":    65,
		"wind_speed":  12,
		"wind_unit":   "km/h",
		"forecast":    "Expected to remain partly cloudy throughout the day",
		"timestamp":   time.Now().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
	log.Printf("[WEATHER] Sent response for %s", location)
}

func handleCalculator(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var payload WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	log.Printf("[CALCULATOR] Received request:")
	log.Printf("  Function: %s", payload.FunctionName)
	log.Printf("  Arguments: %v", payload.Arguments)

	// Extract operation and numbers
	operation := ""
	if op, ok := payload.Arguments["operation"].(string); ok {
		operation = strings.ToLower(op)
	}

	var a, b float64
	if num, ok := payload.Arguments["a"].(float64); ok {
		a = num
	}
	if num, ok := payload.Arguments["b"].(float64); ok {
		b = num
	}

	var result float64
	var err2 error
	switch operation {
	case "add", "+":
		result = a + b
	case "subtract", "-":
		result = a - b
	case "multiply", "*":
		result = a * b
	case "divide", "/":
		if b == 0 {
			err2 = fmt.Errorf("division by zero")
		} else {
			result = a / b
		}
	default:
		err2 = fmt.Errorf("unknown operation: %s", operation)
	}

	response := map[string]interface{}{
		"operation": operation,
		"a":         a,
		"b":         b,
	}

	if err2 != nil {
		response["error"] = err2.Error()
	} else {
		response["result"] = result
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
	log.Printf("[CALCULATOR] %s(%v, %v) = %v", operation, a, b, result)
}

func handleEcho(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var payload WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	log.Printf("[ECHO] Received request:")
	log.Printf("  Function: %s", payload.FunctionName)
	log.Printf("  Arguments: %v", payload.Arguments)
	log.Printf("  Headers: %v", r.Header)

	response := map[string]interface{}{
		"echoed_arguments": payload.Arguments,
		"function_name":    payload.FunctionName,
		"function_id":      payload.FunctionID,
		"tenant_id":        payload.TenantID,
		"correlation_id":   payload.CorrelationID,
		"received_at":      time.Now().Format(time.RFC3339),
		"headers": map[string]string{
			"X-Request-ID":     r.Header.Get("X-Request-ID"),
			"X-Correlation-ID": r.Header.Get("X-Correlation-ID"),
			"X-Tenant-ID":      r.Header.Get("X-Tenant-ID"),
			"X-Function-ID":    r.Header.Get("X-Function-ID"),
			"X-Function-Name":  r.Header.Get("X-Function-Name"),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
	log.Printf("[ECHO] Sent echo response")
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var payload WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	log.Printf("[SEARCH] Received request:")
	log.Printf("  Function: %s", payload.FunctionName)
	log.Printf("  Arguments: %v", payload.Arguments)

	query := ""
	if q, ok := payload.Arguments["query"].(string); ok {
		query = q
	}

	// Simulate search results
	response := map[string]interface{}{
		"query": query,
		"results": []map[string]interface{}{
			{
				"title":   fmt.Sprintf("Result 1 for: %s", query),
				"url":     "https://example.com/result1",
				"snippet": "This is a simulated search result that matches your query.",
			},
			{
				"title":   fmt.Sprintf("Result 2 for: %s", query),
				"url":     "https://example.com/result2",
				"snippet": "Another relevant result from the simulated search.",
			},
			{
				"title":   fmt.Sprintf("Result 3 for: %s", query),
				"url":     "https://example.com/result3",
				"snippet": "A third search result with useful information.",
			},
		},
		"total_results": 3,
		"timestamp":     time.Now().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
	log.Printf("[SEARCH] Sent results for query: %s", query)
}
