package attributes

import (
	"fmt"
	"strings"
)

// CalculateTokenEfficiencyMetrics computes tokens/second and cost/token
func CalculateTokenEfficiencyMetrics(totalTokens int64, latencyMs int64, totalCost float64) (tokensPerSec float64, costPerToken float64) {
	if latencyMs <= 0 || totalTokens <= 0 {
		return 0, 0
	}

	// Calculate tokens per second
	latencySec := float64(latencyMs) / 1000.0
	tokensPerSec = float64(totalTokens) / latencySec

	// Calculate cost per token if cost is available
	if totalCost > 0 {
		costPerToken = totalCost / float64(totalTokens)
	}

	return tokensPerSec, costPerToken
}

// ClassifyError determines error type and retryability based on error message
func ClassifyError(err error) (errorType string, retryable bool) {
	if err == nil {
		return "", false
	}

	errMsg := strings.ToLower(err.Error())

	// Network/connection errors - retryable
	if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "connection") ||
		strings.Contains(errMsg, "network") || strings.Contains(errMsg, "dial") {
		return "network_error", true
	}

	// Rate limit errors - retryable
	if strings.Contains(errMsg, "rate limit") || strings.Contains(errMsg, "429") ||
		strings.Contains(errMsg, "too many requests") {
		return "rate_limit_error", true
	}

	// Server errors (5xx) - retryable
	if strings.Contains(errMsg, "500") || strings.Contains(errMsg, "502") ||
		strings.Contains(errMsg, "503") || strings.Contains(errMsg, "504") ||
		strings.Contains(errMsg, "internal server error") || strings.Contains(errMsg, "service unavailable") {
		return "server_error", true
	}

	// Authentication errors - not retryable
	if strings.Contains(errMsg, "unauthorized") || strings.Contains(errMsg, "401") ||
		strings.Contains(errMsg, "forbidden") || strings.Contains(errMsg, "403") ||
		strings.Contains(errMsg, "invalid api key") || strings.Contains(errMsg, "authentication") {
		return "auth_error", false
	}

	// Validation errors - not retryable
	if strings.Contains(errMsg, "invalid") || strings.Contains(errMsg, "validation") ||
		strings.Contains(errMsg, "bad request") || strings.Contains(errMsg, "400") {
		return "validation_error", false
	}

	// Context errors
	if strings.Contains(errMsg, "context canceled") || strings.Contains(errMsg, "context deadline exceeded") {
		return "context_error", false
	}

	// Default to unknown error, not retryable
	return "unknown_error", false
}

// FormatErrorType creates a human-readable error type string
func FormatErrorType(err error) string {
	errorType, _ := ClassifyError(err)
	return errorType
}

// IsRetryableError checks if an error is retryable
func IsRetryableError(err error) bool {
	_, retryable := ClassifyError(err)
	return retryable
}

// CalculateStreamingStats computes statistics from chunk data
func CalculateStreamingStats(chunkSizes []int) (min, max, avg int, total int) {
	if len(chunkSizes) == 0 {
		return 0, 0, 0, 0
	}

	min, max = chunkSizes[0], chunkSizes[0]
	total = 0

	for _, size := range chunkSizes {
		if size < min {
			min = size
		}
		if size > max {
			max = size
		}
		total += size
	}

	avg = total / len(chunkSizes)
	return min, max, avg, total
}

// CalculateInterChunkLatency computes average time between chunks
func CalculateInterChunkLatency(chunkTimestamps []int64) int64 {
	if len(chunkTimestamps) < 2 {
		return 0
	}

	var totalLatency int64
	for i := 1; i < len(chunkTimestamps); i++ {
		totalLatency += chunkTimestamps[i] - chunkTimestamps[i-1]
	}

	return totalLatency / int64(len(chunkTimestamps)-1)
}

// FormatCostUSD formats a cost value as a USD string
func FormatCostUSD(cost float64) string {
	return fmt.Sprintf("$%.6f", cost)
}

// FormatTokensPerSecond formats tokens/second metric
func FormatTokensPerSecond(tokensPerSec float64) string {
	return fmt.Sprintf("%.2f tokens/s", tokensPerSec)
}
