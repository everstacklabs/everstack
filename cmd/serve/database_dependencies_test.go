package serve

import (
	"context"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
)

func TestHybridDatabaseDependenciesTreatOptionalClickHouseAsNonRequired(t *testing.T) {
	t.Parallel()

	checks := databaseDependencyChecks(&validator.Config{Database: &validator.DatabaseConfig{
		Mode: "hybrid",
		Postgres: validator.PostgresDB{
			DSN: "postgres://primary",
		},
		ClickHouse: validator.ClickHouseDB{
			DSN:      "clickhouse://analytics",
			Optional: true,
		},
	}})

	if len(checks) != 2 {
		t.Fatalf("dependency count = %d, want 2", len(checks))
	}
	if checks[0].Name != "postgres" || !checks[0].Required {
		t.Fatalf("Postgres dependency = %#v, want required", checks[0])
	}
	if checks[1].Name != "clickhouse" || checks[1].Required {
		t.Fatalf("ClickHouse dependency = %#v, want optional", checks[1])
	}
}

func TestGatewayDependenciesRequireConfiguredRedis(t *testing.T) {
	t.Parallel()

	checks := gatewayDependencyChecks(&validator.Config{Cache: &validator.CacheConfig{
		Type:  "redis",
		Redis: validator.RedisCacheConfig{Required: true},
	}}, nil)
	if len(checks) != 1 || checks[0].Name != "redis" || !checks[0].Required {
		t.Fatalf("dependencies = %#v, want one required Redis check", checks)
	}
	if err := checks[0].Check(context.Background()); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("Redis dependency error = %v, want not initialized", err)
	}
}

func TestSinglePostgresHasRequiredDependency(t *testing.T) {
	t.Parallel()

	checks := databaseDependencyChecks(&validator.Config{Database: &validator.DatabaseConfig{
		Mode: "single",
		Type: "postgres",
		Postgres: validator.PostgresDB{
			DSN: "postgres://primary",
		},
	}})

	if len(checks) != 1 || checks[0].Name != "postgres" || !checks[0].Required {
		t.Fatalf("dependencies = %#v, want one required Postgres check", checks)
	}
}
