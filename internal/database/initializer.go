package database

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/database/dialect"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Environment variable names for database DSN overrides
const (
	EnvPostgresDSN   = "EVS_POSTGRES_DSN"
	EnvClickHouseDSN = "EVS_CLICKHOUSE_DSN"
)

// getPostgresDSN returns the Postgres DSN, preferring environment variable over config
func getPostgresDSN(cfg *validator.Config) string {
	if envDSN := os.Getenv(EnvPostgresDSN); envDSN != "" {
		logger.Debugf("Using Postgres DSN from %s environment variable", EnvPostgresDSN)
		return envDSN
	}
	if cfg.Database != nil && cfg.Database.Postgres.DSN != "" {
		return cfg.Database.Postgres.DSN
	}
	return ""
}

// getClickHouseDSN returns the ClickHouse DSN, preferring environment variable over config
func getClickHouseDSN(cfg *validator.Config) string {
	if envDSN := os.Getenv(EnvClickHouseDSN); envDSN != "" {
		logger.Debugf("Using ClickHouse DSN from %s environment variable", EnvClickHouseDSN)
		return envDSN
	}
	if cfg.Database != nil && cfg.Database.ClickHouse.DSN != "" {
		return cfg.Database.ClickHouse.DSN
	}
	return ""
}

// InitResult holds opened connections after initialization.
type InitResult struct {
	Mode      string
	Primary   *Conn    // primary DB connection (RW)
	Analytics *sqlx.DB // analytics ClickHouse DB (hybrid mode)
}

// InitializeFromConfig opens databases based on server configuration, pings them,
// ensures schema (where applicable), and returns opened handles.
func InitializeFromConfig(ctx context.Context, cfg *validator.Config) (*InitResult, error) {
	if cfg == nil {
		return &InitResult{Mode: ""}, nil
	}
	mode := strings.ToLower(cfg.Database.Mode)
	if mode == "hybrid" {
		return initHybrid(ctx, cfg)
	}
	return initSingle(ctx, cfg)
}

func initSingle(ctx context.Context, cfg *validator.Config) (*InitResult, error) {
	// default to postgres if unspecified
	typeStr := "postgres"
	if cfg.Database != nil && cfg.Database.Type != "" {
		typeStr = strings.ToLower(cfg.Database.Type)
	}

	var dbc Config
	switch typeStr {
	case "postgres":
		dbc = Config{Type: TypePostgres, DSN: getPostgresDSN(cfg), MaxOpen: cfg.Database.Postgres.MaxOpen, MaxIdle: cfg.Database.Postgres.MaxIdle}
	case "clickhouse":
		dbc = Config{Type: TypeClickHouse, DSN: getClickHouseDSN(cfg), MaxOpen: cfg.Database.ClickHouse.MaxOpen, MaxIdle: cfg.Database.ClickHouse.MaxIdle}
	case "memory":
		dbc = Config{Type: TypeMemory}
	default:
		logger.Warnf("unknown database.type=%s; defaulting to postgres", typeStr)
		dbc = Config{Type: TypePostgres, DSN: getPostgresDSN(cfg), MaxOpen: cfg.Database.Postgres.MaxOpen, MaxIdle: cfg.Database.Postgres.MaxIdle}
	}

	if dbc.Type == "" {
		return &InitResult{Mode: "single"}, nil
	}

	conn, err := Open(ctx, dbc)
	if err != nil {
		return nil, fmt.Errorf("open %s connection: %w", dbc.Type, err)
	}
	// Ping + schema ensure where applicable
	if conn.RW != nil {
		pingCtx, pingCancel := context.WithTimeout(ctx, 10*time.Second)
		defer pingCancel()
		if err := conn.RW.PingContext(pingCtx); err != nil {
			return nil, fmt.Errorf("%s ping failed: %w", dbc.Type, err)
		}
		var d dialect.Dialect
		if dbc.Type == TypePostgres {
			d = dialect.Postgres{}
		}
		if dbc.Type == TypeClickHouse {
			d = dialect.ClickHouse{}
		}
		if d != nil {
			logger.Infof("running database migrations and validation for %s...", dbc.Type)
			if err := d.EnsureSchema(ctx, conn.RW); err != nil {
				return nil, fmt.Errorf("ensure schema for %s: %w", dbc.Type, err)
			}
			logger.Infof("database migrations applied and schema verified: %s", dbc.Type)
		}
	}

	logger.Debugf("database configured: %s", dbc.Type)
	return &InitResult{Mode: "single", Primary: conn}, nil
}

func initHybrid(ctx context.Context, cfg *validator.Config) (*InitResult, error) {
	// Primary: Postgres
	pgDSN := getPostgresDSN(cfg)
	pgCfg := Config{Type: TypePostgres, DSN: pgDSN, MaxOpen: cfg.Database.Postgres.MaxOpen, MaxIdle: cfg.Database.Postgres.MaxIdle}
	pgConn, err := Open(ctx, pgCfg)
	if err != nil {
		return nil, fmt.Errorf("open postgres connection: %w", err)
	}
	if pgConn.RW != nil {
		pingCtx, pingCancel := context.WithTimeout(ctx, 10*time.Second)
		defer pingCancel()
		if err := pgConn.RW.PingContext(pingCtx); err != nil {
			return nil, fmt.Errorf("postgres ping failed: %w", err)
		}
		if err := (dialect.Postgres{}).EnsureSchema(ctx, pgConn.RW); err != nil {
			return nil, fmt.Errorf("postgres schema ensure failed: %w", err)
		}
		logger.Infof("postgres migrations applied and schema verified")
	}

	// Analytics: ClickHouse
	dsn := getClickHouseDSN(cfg)
	if dsn == "" {
		return nil, fmt.Errorf("database.mode=hybrid requires database.clickhouse.dsn to be set (via config or %s env var)", EnvClickHouseDSN)
	}
	chConn, err := Open(ctx, Config{Type: TypeClickHouse, DSN: dsn, MaxOpen: cfg.Database.ClickHouse.MaxOpen, MaxIdle: cfg.Database.ClickHouse.MaxIdle})
	if err != nil {
		return nil, fmt.Errorf("open clickhouse connection: %w", err)
	}
	var chx *sqlx.DB
	if chConn.RW != nil {
		chPingCtx, chPingCancel := context.WithTimeout(ctx, 10*time.Second)
		defer chPingCancel()
		if err := chConn.RW.PingContext(chPingCtx); err != nil {
			return nil, fmt.Errorf("clickhouse ping failed: %w", err)
		}
		if err := (dialect.ClickHouse{}).EnsureSchema(ctx, chConn.RW); err != nil {
			return nil, fmt.Errorf("clickhouse schema ensure failed: %w", err)
		}
		logger.Infof("clickhouse migrations applied and schema verified")
		chx = chConn.RW
	}

	return &InitResult{Mode: "hybrid", Primary: pgConn, Analytics: chx}, nil
}
