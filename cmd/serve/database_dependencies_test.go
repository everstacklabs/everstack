package serve

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jmoiron/sqlx"

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
	}}, dependencyPools{})

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
	}}, dependencyPools{}, nil)
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
	}}, dependencyPools{})

	if len(checks) != 1 || checks[0].Name != "postgres" || !checks[0].Required {
		t.Fatalf("dependencies = %#v, want one required Postgres check", checks)
	}
}

type pingCountingConnector struct {
	opens atomic.Int32
	fail  bool
}

func (c *pingCountingConnector) Connect(context.Context) (driver.Conn, error) {
	c.opens.Add(1)
	if c.fail {
		return nil, errors.New("down")
	}
	return pingConn{}, nil
}
func (c *pingCountingConnector) Driver() driver.Driver { return nil }

type pingConn struct{}

func (pingConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unsupported") }
func (pingConn) Close() error                        { return nil }
func (pingConn) Begin() (driver.Tx, error)           { return nil, errors.New("unsupported") }

// Readiness must reuse the open pool: repeated probes share one connection
// instead of dialling a new one per check.
func TestDatabaseDependencyChecksPingExistingPool(t *testing.T) {
	t.Parallel()

	conn := &pingCountingConnector{}
	db := sqlx.NewDb(sql.OpenDB(conn), "fake")
	t.Cleanup(func() { _ = db.Close() })

	checks := databaseDependencyChecks(&validator.Config{Database: &validator.DatabaseConfig{
		Mode:     "single",
		Type:     "postgres",
		Postgres: validator.PostgresDB{DSN: "postgres://primary"},
	}}, dependencyPools{Primary: db})
	for i := 0; i < 5; i++ {
		if err := checks[0].Check(context.Background()); err != nil {
			t.Fatalf("check %d: %v", i, err)
		}
	}
	if got := conn.opens.Load(); got != 1 {
		t.Fatalf("connections opened = %d, want 1 (pool reused)", got)
	}
}

func TestDatabaseDependencyChecksReportDownAndMissingPools(t *testing.T) {
	t.Parallel()

	down := sqlx.NewDb(sql.OpenDB(&pingCountingConnector{fail: true}), "fake")
	t.Cleanup(func() { _ = down.Close() })
	cfg := &validator.Config{Database: &validator.DatabaseConfig{
		Mode:       "hybrid",
		Postgres:   validator.PostgresDB{DSN: "postgres://primary"},
		ClickHouse: validator.ClickHouseDB{DSN: "clickhouse://analytics", Optional: true},
	}}
	checks := databaseDependencyChecks(cfg, dependencyPools{Primary: down})
	if err := checks[0].Check(context.Background()); err == nil {
		t.Fatal("postgres check passed against a down pool")
	}
	if err := checks[1].Check(context.Background()); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("clickhouse check error = %v, want not initialized", err)
	}
	if checks[1].Required {
		t.Fatal("optional ClickHouse must not be required for readiness")
	}
}
