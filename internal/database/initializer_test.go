package database

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
)

func TestInitHybridAllowsOptionalClickHouseFailure(t *testing.T) {
	t.Parallel()

	cfg := &validator.Config{Database: &validator.DatabaseConfig{
		Mode: "hybrid",
		Postgres: validator.PostgresDB{
			DSN: "postgres://primary",
		},
		ClickHouse: validator.ClickHouseDB{
			DSN:      "clickhouse://analytics",
			Optional: true,
		},
	}}

	primary := &Conn{Type: TypePostgres}
	open := func(_ context.Context, cfg Config) (*Conn, error) {
		switch cfg.Type {
		case TypePostgres:
			return primary, nil
		case TypeClickHouse:
			return nil, errors.New("analytics unavailable")
		default:
			t.Fatalf("unexpected database type %q", cfg.Type)
			return nil, nil
		}
	}

	result, err := initHybridWithOpen(context.Background(), cfg, open)
	if err != nil {
		t.Fatalf("initHybridWithOpen() error = %v", err)
	}
	if result.Primary != primary {
		t.Fatal("primary connection was not preserved")
	}
	if result.Analytics != nil {
		t.Fatal("analytics connection must be nil while ClickHouse is unavailable")
	}
}

func TestInitHybridStillRequiresPostgres(t *testing.T) {
	t.Parallel()

	cfg := &validator.Config{Database: &validator.DatabaseConfig{
		Mode: "hybrid",
		Postgres: validator.PostgresDB{
			DSN: "postgres://primary",
		},
		ClickHouse: validator.ClickHouseDB{
			DSN:      "clickhouse://analytics",
			Optional: true,
		},
	}}

	open := func(_ context.Context, cfg Config) (*Conn, error) {
		if cfg.Type != TypePostgres {
			t.Fatalf("ClickHouse must not be opened after Postgres fails")
		}
		return nil, errors.New("primary unavailable")
	}

	_, err := initHybridWithOpen(context.Background(), cfg, open)
	if err == nil || !strings.Contains(err.Error(), "open postgres connection") {
		t.Fatalf("initHybridWithOpen() error = %v, want required Postgres error", err)
	}
}

func TestInitHybridRequiresClickHouseByDefault(t *testing.T) {
	t.Parallel()

	cfg := &validator.Config{Database: &validator.DatabaseConfig{
		Mode: "hybrid",
		Postgres: validator.PostgresDB{
			DSN: "postgres://primary",
		},
		ClickHouse: validator.ClickHouseDB{
			DSN: "clickhouse://analytics",
		},
	}}

	primaryClosed := false
	open := func(_ context.Context, cfg Config) (*Conn, error) {
		if cfg.Type == TypePostgres {
			return &Conn{Type: TypePostgres, CloseF: func(context.Context) error {
				primaryClosed = true
				return nil
			}}, nil
		}
		return nil, errors.New("analytics unavailable")
	}

	_, err := initHybridWithOpen(context.Background(), cfg, open)
	if err == nil || !strings.Contains(err.Error(), "open clickhouse connection") {
		t.Fatalf("initHybridWithOpen() error = %v, want required ClickHouse error", err)
	}
	if !primaryClosed {
		t.Fatal("primary connection was leaked after required ClickHouse failed")
	}
}
