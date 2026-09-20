package validator

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadConfigDecodesRedisSentinelEnvironment(t *testing.T) {
	t.Setenv("EVS_CACHE_REDIS_SENTINEL_MASTER_NAME", "everstack")
	t.Setenv("EVS_CACHE_REDIS_SENTINEL_ADDRESSES", "redis-0:26379,redis-1:26379,redis-2:26379")
	t.Setenv("EVS_CACHE_REDIS_REQUIRED", "true")
	t.Setenv("EVS_CACHE_REDIS_TLS_ENABLED", "true")
	t.Setenv("EVS_CACHE_REDIS_TLS_CA_FILE", "/etc/everstack/redis-tls/ca.crt")
	t.Setenv("EVS_CACHE_REDIS_TLS_SERVER_NAME", "ha-data-redis")
	configPath := filepath.Join(t.TempDir(), "gateway.yaml")
	if err := os.WriteFile(configPath, []byte("cache:\n  enabled: true\n  type: redis\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	want := []string{"redis-0:26379", "redis-1:26379", "redis-2:26379"}
	if config.Cache.Redis.SentinelMasterName != "everstack" {
		t.Fatalf("SentinelMasterName = %q", config.Cache.Redis.SentinelMasterName)
	}
	if !reflect.DeepEqual(config.Cache.Redis.SentinelAddresses, want) {
		t.Fatalf("SentinelAddresses = %#v, want %#v", config.Cache.Redis.SentinelAddresses, want)
	}
	if !config.Cache.Redis.TLSEnabled || config.Cache.Redis.TLSCAFile != "/etc/everstack/redis-tls/ca.crt" || config.Cache.Redis.TLSServerName != "ha-data-redis" {
		t.Fatalf("Redis TLS config = %#v", config.Cache.Redis)
	}
	if !config.Cache.Redis.Required {
		t.Fatal("Redis should be required")
	}
}
