package cache

import (
	"testing"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
)

func TestRedisConnectionMode(t *testing.T) {
	tests := []struct {
		name    string
		config  validator.RedisCacheConfig
		want    redisConnectionMode
		wantErr bool
	}{
		{
			name:   "direct address",
			config: validator.RedisCacheConfig{Address: "redis:6379"},
			want:   redisConnectionDirect,
		},
		{
			name: "sentinel without direct address",
			config: validator.RedisCacheConfig{
				SentinelMasterName: "everstack",
				SentinelAddresses:  []string{"redis-0:26379", "redis-1:26379", "redis-2:26379"},
			},
			want: redisConnectionSentinel,
		},
		{
			name: "sentinel addresses require master",
			config: validator.RedisCacheConfig{
				SentinelAddresses: []string{"redis:26379"},
			},
			wantErr: true,
		},
		{
			name: "sentinel master requires addresses",
			config: validator.RedisCacheConfig{
				SentinelMasterName: "everstack",
			},
			wantErr: true,
		},
		{name: "missing configuration", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := connectionMode(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("connectionMode() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("connectionMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRedisFailoverOptionsUsePasswordForRedisAndSentinel(t *testing.T) {
	cfg := validator.RedisCacheConfig{
		SentinelMasterName: "everstack",
		SentinelAddresses:  []string{"redis:26379"},
		Password:           "shared-password",
		DB:                 2,
		PoolSize:           12,
	}

	opts := newRedisFailoverOptions(cfg, cfg.PoolSize, nil)
	if opts.Password != cfg.Password {
		t.Fatalf("Redis password = %q, want configured password", opts.Password)
	}
	if opts.SentinelPassword != cfg.Password {
		t.Fatalf("Sentinel password = %q, want configured password", opts.SentinelPassword)
	}
	if opts.MasterName != cfg.SentinelMasterName {
		t.Fatalf("master name = %q, want %q", opts.MasterName, cfg.SentinelMasterName)
	}
}
