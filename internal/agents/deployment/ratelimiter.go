package deployment

import (
	"sync"
	"time"
)

// DeploymentRateLimiter provides per-deployment in-memory rate limiting using a token bucket.
type DeploymentRateLimiter struct {
	mu       sync.RWMutex
	counters map[string]*rlCounter
}

type rlCounter struct {
	tokens    float64
	maxTokens float64
	refillPer float64 // tokens per second
	lastTime  time.Time
}

// NewDeploymentRateLimiter creates a new rate limiter.
func NewDeploymentRateLimiter() *DeploymentRateLimiter {
	return &DeploymentRateLimiter{
		counters: make(map[string]*rlCounter),
	}
}

// Allow returns true if the request is allowed under the deployment's rate limit.
// rpm is the max requests per minute; burst is the max burst size (defaults to rpm if 0).
func (rl *DeploymentRateLimiter) Allow(deploymentID string, rpm, burst int) bool {
	if rpm <= 0 {
		return true // no limit configured
	}
	if burst <= 0 {
		burst = rpm
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	c, ok := rl.counters[deploymentID]
	if !ok {
		c = &rlCounter{
			tokens:    float64(burst),
			maxTokens: float64(burst),
			refillPer: float64(rpm) / 60.0, // tokens per second
			lastTime:  now,
		}
		rl.counters[deploymentID] = c
	}

	// Update max if config changed
	c.maxTokens = float64(burst)
	c.refillPer = float64(rpm) / 60.0

	// Refill tokens based on elapsed time
	elapsed := now.Sub(c.lastTime).Seconds()
	c.tokens += elapsed * c.refillPer
	if c.tokens > c.maxTokens {
		c.tokens = c.maxTokens
	}
	c.lastTime = now

	if c.tokens < 1.0 {
		return false
	}
	c.tokens--
	return true
}

// ConcurrencyLimiter tracks concurrent sessions per deployment.
type ConcurrencyLimiter struct {
	mu       sync.Mutex
	counters map[string]int
}

// NewConcurrencyLimiter creates a new concurrency limiter.
func NewConcurrencyLimiter() *ConcurrencyLimiter {
	return &ConcurrencyLimiter{
		counters: make(map[string]int),
	}
}

// Acquire attempts to acquire a concurrency slot. Returns true if allowed.
func (cl *ConcurrencyLimiter) Acquire(deploymentID string, max int) bool {
	if max <= 0 {
		return true
	}
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.counters[deploymentID] >= max {
		return false
	}
	cl.counters[deploymentID]++
	return true
}

// Release releases a concurrency slot.
func (cl *ConcurrencyLimiter) Release(deploymentID string) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.counters[deploymentID] > 0 {
		cl.counters[deploymentID]--
	}
}
