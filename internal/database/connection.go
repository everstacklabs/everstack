package database

import (
	"context"
	"fmt"
	"strings"
	"time"

	_ "github.com/ClickHouse/clickhouse-go/v2"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

// Conn provides read/write interfaces; for SQL backends, both use the same pool.
type Conn struct {
	Type   Type
	RW     *sqlx.DB // SQL backends
	RO     *sqlx.DB // Optional read replica (future)
	CloseF func(context.Context) error
}

func (c *Conn) Close(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if c.CloseF != nil {
		return c.CloseF(ctx)
	}
	if c.RW != nil {
		return c.RW.Close()
	}
	return nil
}

// Config is a simplified connection config to be loaded from gateway.yaml later.
type Config struct {
	Type        Type
	DSN         string
	MaxOpen     int
	MaxIdle     int
	MaxLifetime time.Duration
}

// Open creates a connection based on config.
func Open(ctx context.Context, cfg Config) (*Conn, error) {
	switch cfg.Type {
	case TypePostgres:
		dsn := cfg.DSN
		// Set default search_path so unqualified table names resolve to the
		// everstack schema. In shared/multi-tenant mode the tenant middleware
		// overrides this per-request on a dedicated connection.
		if !strings.Contains(dsn, "search_path") {
			if strings.Contains(dsn, "?") {
				dsn += "&search_path=everstack,public"
			} else {
				dsn += "?search_path=everstack,public"
			}
		}
		db, err := sqlx.Open("pgx", dsn)
		if err != nil {
			return nil, fmt.Errorf("postgres open: %w", err)
		}
		tune(db, cfg)
		return &Conn{Type: cfg.Type, RW: db}, nil
	case TypeClickHouse:
		db, err := sqlx.Open("clickhouse", cfg.DSN)
		if err != nil {
			return nil, fmt.Errorf("clickhouse open: %w", err)
		}
		tune(db, cfg)
		return &Conn{Type: cfg.Type, RW: db}, nil
	case TypeMemory:
		// In-memory placeholder; no external connection
		return &Conn{Type: cfg.Type, CloseF: func(context.Context) error { return nil }}, nil
	default:
		return nil, fmt.Errorf("unknown database type: %s", cfg.Type)
	}
}

func tune(db *sqlx.DB, cfg Config) {
	if db == nil {
		return
	}
	if cfg.MaxOpen > 0 {
		db.SetMaxOpenConns(cfg.MaxOpen)
	}
	if cfg.MaxIdle > 0 {
		db.SetMaxIdleConns(cfg.MaxIdle)
	}
	if cfg.MaxLifetime > 0 {
		db.SetConnMaxLifetime(cfg.MaxLifetime)
	}
}
