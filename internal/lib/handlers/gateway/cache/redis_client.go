// Package cache provides caching implementations for the gateway.
package cache

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/redis/go-redis/v9"
)

// RedisClient wraps a Redis client with health checking and RedisSearch support
type RedisClient struct {
	client    *redis.Client
	config    validator.RedisCacheConfig
	connected atomic.Bool
}

// NewRedisClient creates a new Redis client with the given configuration
func NewRedisClient(cfg validator.RedisCacheConfig) (*RedisClient, error) {
	if cfg.Address == "" {
		return nil, fmt.Errorf("redis address is required")
	}

	logger.WithFields(
		"address", cfg.Address,
		"db", cfg.DB,
		"password_set", cfg.Password != "",
		"search_enabled", cfg.Search.Enabled,
	).Info("Attempting to connect to Redis")

	// Set default pool size if not specified
	poolSize := cfg.PoolSize
	if poolSize == 0 {
		poolSize = 100
	}

	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Address,
		Password: cfg.Password,
		DB:       cfg.DB,
		PoolSize: poolSize,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis connection failed to %s: %w", cfg.Address, err)
	}

	rc := &RedisClient{
		client: client,
		config: cfg,
	}
	rc.connected.Store(true)

	logger.WithFields(
		"address", cfg.Address,
		"db", cfg.DB,
		"pool_size", poolSize,
	).Info("Redis client connected successfully")

	// Check for RedisSearch module if enabled
	if cfg.Search.Enabled {
		if err := rc.validateRedisSearch(ctx); err != nil {
			return nil, fmt.Errorf("RedisSearch validation failed: %w", err)
		}
		logger.WithFields("index_name", cfg.Search.IndexName).Info("RedisSearch module validated")
	}

	return rc, nil
}

// validateRedisSearch checks if the RedisSearch module is loaded
func (rc *RedisClient) validateRedisSearch(ctx context.Context) error {
	// Log connection details for debugging
	logger.WithFields(
		"address", rc.config.Address,
		"db", rc.config.DB,
	).Debug("Validating RedisSearch module")

	// Check if RedisSearch module is loaded
	result, err := rc.client.Do(ctx, "MODULE", "LIST").Result()
	if err != nil {
		return fmt.Errorf("failed to list Redis modules: %w", err)
	}

	logger.WithFields("result_type", fmt.Sprintf("%T", result)).Debug("MODULE LIST result type")

	// Parse module list - can be []interface{} or string
	var modules []interface{}

	switch v := result.(type) {
	case []interface{}:
		modules = v
	case string:
		// Some Redis clients return string, try to parse it
		logger.WithFields("result_str", v).Debug("MODULE LIST returned string")
		// If it's a string and contains "search", consider it valid
		if strings.Contains(strings.ToLower(v), "search") {
			logger.Info("RedisSearch module found (string response)")
			return nil
		}
		return fmt.Errorf("unexpected string response from MODULE LIST")
	default:
		logger.WithFields("result", result, "type", fmt.Sprintf("%T", result)).Warn("Unexpected module list format")
		return fmt.Errorf("unexpected module list format: %T", result)
	}

	logger.WithFields("num_modules", len(modules)).Debug("Checking Redis modules")

	// Look for RedisSearch or search module
	var foundModules []string
	for idx, mod := range modules {
		logger.WithFields("idx", idx, "mod_type", fmt.Sprintf("%T", mod), "mod_value", fmt.Sprintf("%v", mod)).Debug("Processing module entry")

		// Try map format first (Redis Stack format) - check both possible map types
		var modMap map[string]interface{}
		var ok bool

		// Try map[string]interface{}
		if modMap, ok = mod.(map[string]interface{}); !ok {
			// Try map[interface{}]interface{} (some Redis clients use this)
			if m, ok2 := mod.(map[interface{}]interface{}); ok2 {
				// Convert to map[string]interface{}
				modMap = make(map[string]interface{})
				for k, v := range m {
					if ks, ok3 := k.(string); ok3 {
						modMap[ks] = v
					}
				}
				ok = true
			}
		}

		if ok && modMap != nil {
			logger.WithFields("idx", idx, "map_keys", fmt.Sprintf("%v", modMap)).Debug("Found map format module")
			if name, ok := modMap["name"].(string); ok {
				foundModules = append(foundModules, name)
				logger.WithFields("module_name", name).Debug("Found module name in map")
				// Check for search, ft, or redisearch module names
				nameLower := strings.ToLower(name)
				if strings.Contains(nameLower, "search") || nameLower == "ft" {
					logger.WithFields("module_name", name).Info("RedisSearch module found")
					return nil
				}
			} else {
				logger.WithFields("idx", idx, "name_value", modMap["name"], "name_type", fmt.Sprintf("%T", modMap["name"])).Warn("Could not extract name from map")
			}
			continue
		}

		// Try array format (older Redis format)
		modInfo, ok := mod.([]interface{})
		if !ok {
			// Try string directly
			if modStr, ok := mod.(string); ok {
				logger.WithFields("module_entry", modStr).Debug("Found module string entry")
				if strings.Contains(strings.ToLower(modStr), "search") {
					logger.WithFields("module_name", modStr).Info("RedisSearch module found")
					return nil
				}
			}
			continue
		}

		// Parse structured module info (array format)
		for i, field := range modInfo {
			if fieldStr, ok := field.(string); ok {
				foundModules = append(foundModules, fieldStr)
				if fieldStr == "name" && i+1 < len(modInfo) {
					if name, ok := modInfo[i+1].(string); ok {
						foundModules = append(foundModules, name)
						// Check for search, ft, or redisearch module names
						nameLower := strings.ToLower(name)
						if strings.Contains(nameLower, "search") || nameLower == "ft" {
							logger.WithFields("module_name", name).Info("RedisSearch module found")
							return nil
						}
					}
				} else if strings.Contains(strings.ToLower(fieldStr), "search") {
					// Direct match in field
					logger.WithFields("module_name", fieldStr).Info("RedisSearch module found")
					return nil
				}
			}
		}
	}

	logger.WithFields("found_modules", foundModules, "raw_result", fmt.Sprintf("%v", result)).Warn("RedisSearch module not found in module list")
	return fmt.Errorf("RedisSearch module not found. Please install RedisSearch: https://redis.io/docs/stack/search/")
}

// Client returns the underlying Redis client
func (rc *RedisClient) Client() *redis.Client {
	return rc.client
}

// IsConnected returns whether the Redis client is connected
func (rc *RedisClient) IsConnected() bool {
	return rc.connected.Load()
}

// Ping checks if the Redis connection is alive
func (rc *RedisClient) Ping(ctx context.Context) error {
	if !rc.connected.Load() {
		return fmt.Errorf("redis client not connected")
	}

	if err := rc.client.Ping(ctx).Err(); err != nil {
		rc.connected.Store(false)
		return fmt.Errorf("redis ping failed: %w", err)
	}

	return nil
}

// Close closes the Redis connection
func (rc *RedisClient) Close() error {
	rc.connected.Store(false)
	return rc.client.Close()
}

// Do executes a Redis command
func (rc *RedisClient) Do(ctx context.Context, args ...interface{}) *redis.Cmd {
	return rc.client.Do(ctx, args...)
}

// Config returns the Redis configuration
func (rc *RedisClient) Config() validator.RedisCacheConfig {
	return rc.config
}
