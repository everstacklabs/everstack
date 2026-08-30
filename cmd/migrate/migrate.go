package migrate

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/database/migrations"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func New() *cobra.Command {
	// Ensure CLI logs use the same bracket formatter as the server
	logger.SetFormatter(logger.NewDefaultBracketFormatter())
	logger.SetGlobal()

	cmd := &cobra.Command{
		Use:    "migrate",
		Hidden: true,
		Short:  "Database migrations",
	}

	// mf migrate new <name>: create matching migrations for postgres and clickhouse with same timestamp
	cmd.AddCommand(&cobra.Command{
		Use:    "new [name]",
		Short:  "Create a new timestamped migration in both postgres and clickhouse",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			now := time.Now()
			upPG, downPG, err := migrations.NewAt("postgres", name, now)
			if err != nil {
				return err
			}
			upCH, downCH, err := migrations.NewAt("clickhouse", name, now)
			if err != nil {
				return err
			}
			fmt.Printf("created: %s\ncreated: %s\ncreated: %s\ncreated: %s\n", upPG, downPG, upCH, downCH)
			return nil
		},
	})

	cmd.AddCommand(buildDialectCmd("postgres"))
	cmd.AddCommand(buildDialectCmd("clickhouse"))
	return cmd
}

func buildDialectCmd(dialect string) *cobra.Command {
	dc := &cobra.Command{
		Use:   dialect,
		Short: fmt.Sprintf("%s migrations", strings.Title(dialect)),
	}

	// mf migrate <dialect> new <name> [--service]
	newCmd := &cobra.Command{
		Use:    "new [name]",
		Short:  "Create a new timestamped migration",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			service := cmd.Flags().Lookup("service").Value.String()
			if strings.TrimSpace(service) != "" {
				up, down, err := migrations.NewForService(dialect, service, name)
				if err != nil {
					return err
				}
				fmt.Printf("created: %s\ncreated: %s\n", up, down)
				return nil
			}
			up, down, err := migrations.New(dialect, name)
			if err != nil {
				return err
			}
			fmt.Printf("created: %s\ncreated: %s\n", up, down)
			return nil
		},
	}
	newCmd.Flags().String("service", "", "service name for service-scoped path: services/{service}/internal/database/migrations/{dialect}")
	dc.AddCommand(newCmd)

	// mf migrate <dialect> up [--dsn] [--service]
	up := &cobra.Command{
		Use:    "up",
		Short:  "Apply all pending migrations",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dsn := resolveDSN(dialect, cmd)
			if dsn == "" {
				return fmt.Errorf("no DSN found; set database.%s.dsn in config or pass --dsn", dialect)
			}
			if strings.EqualFold(dialect, string(database.TypeMemory)) {
				return fmt.Errorf("memory dialect does not support migrations")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			conn, err := openByDialect(ctx, dialect, dsn)
			if err != nil {
				return err
			}
			defer conn.Close(ctx)
			service := cmd.Flags().Lookup("service").Value.String()
			if strings.TrimSpace(service) != "" {
				return migrations.EnsureForService(ctx, conn.RW, dialect, service)
			}
			return migrations.Ensure(ctx, conn.RW, dialect)
		},
	}
	up.Flags().String("dsn", "", "override connection string (defaults to database.<dialect>.dsn)")
	up.Flags().String("service", "", "service name for service-scoped migrations: services/{service}/internal/database/migrations/{dialect}")
	dc.AddCommand(up)

	// mf migrate <dialect> down [--steps N|--all] [--dsn] [--service]
	var steps int
	var all bool
	down := &cobra.Command{
		Use:    "down",
		Short:  "Rollback migrations",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dsn := resolveDSN(dialect, cmd)
			if dsn == "" {
				return fmt.Errorf("no DSN found; set database.%s.dsn in config or pass --dsn", dialect)
			}
			if strings.EqualFold(dialect, string(database.TypeMemory)) {
				return fmt.Errorf("memory dialect does not support migrations")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			conn, err := openByDialect(ctx, dialect, dsn)
			if err != nil {
				return err
			}
			defer conn.Close(ctx)
			service := cmd.Flags().Lookup("service").Value.String()
			if strings.TrimSpace(service) != "" {
				return migrations.MigrateDownForService(ctx, conn.RW, dialect, service, steps, all)
			}
			return migrations.MigrateDown(ctx, conn.RW, dialect, steps, all)
		},
	}
	down.Flags().IntVar(&steps, "steps", 1, "number of steps to rollback")
	down.Flags().BoolVar(&all, "all", false, "rollback all applied migrations")
	down.Flags().String("dsn", "", "override connection string (defaults to database.<dialect>.dsn)")
	down.Flags().String("service", "", "service name for service-scoped migrations: services/{service}/internal/database/migrations/{dialect}")
	dc.AddCommand(down)

	// mf migrate <dialect> status [--dsn] [--service]
	status := &cobra.Command{
		Use:    "status",
		Short:  "Show applied and pending migrations",
		Hidden: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			dsn := resolveDSN(dialect, cmd)
			if dsn == "" {
				return fmt.Errorf("no DSN found; set database.%s.dsn in config or pass --dsn", dialect)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			conn, err := openByDialect(ctx, dialect, dsn)
			if err != nil {
				return err
			}
			defer conn.Close(ctx)
			appliedMap, err := migrations.LoadAppliedForCLI(ctx, conn.RW, dialect)
			if err != nil {
				return err
			}
			service := cmd.Flags().Lookup("service").Value.String()
			var ups []migrations.Migration
			if strings.TrimSpace(service) != "" {
				ups, err = migrations.LoadServiceDiskForCLI(service, dialect)
			} else {
				ups, err = migrations.LoadDiskForCLI(dialect)
			}
			if err != nil {
				return err
			}
			fmt.Printf("%s migrations\n\n", strings.Title(dialect))
			for _, m := range ups {
				key := fmt.Sprintf("%s:%d", strings.ToLower(dialect), m.Version)
				state := "pending"
				if _, ok := appliedMap[key]; ok {
					state = "applied"
				}
				fmt.Printf("%s_%014d\t%s\n", m.Name, m.Version, state)
			}
			return nil
		},
	}
	status.Flags().String("dsn", "", "override connection string (defaults to database.<dialect>.dsn)")
	status.Flags().String("service", "", "service name for service-scoped migrations: services/{service}/internal/database/migrations/{dialect}")
	dc.AddCommand(status)

	return dc
}

func openByDialect(ctx context.Context, dialect, dsn string) (*database.Conn, error) {
	switch strings.ToLower(dialect) {
	case "postgres":
		return database.Open(ctx, database.Config{Type: database.TypePostgres, DSN: dsn})
	case "clickhouse":
		return database.Open(ctx, database.Config{Type: database.TypeClickHouse, DSN: dsn})
	default:
		return nil, fmt.Errorf("unsupported dialect: %s", dialect)
	}
}

func resolveDSN(dialect string, cmd *cobra.Command) string {
	// explicit flag override
	if f := cmd.Flags().Lookup("dsn"); f != nil {
		if v := f.Value.String(); strings.TrimSpace(v) != "" {
			return v
		}
	}
	// environment variables
	switch strings.ToLower(dialect) {
	case "postgres":
		if env := os.Getenv("EVS_DATABASE_POSTGRES_DSN"); strings.TrimSpace(env) != "" {
			return env
		}
	case "clickhouse":
		if env := os.Getenv("EVS_DATABASE_CLICKHOUSE_DSN"); strings.TrimSpace(env) != "" {
			return env
		}
	}
	// from active viper config (merged via --config)
	switch strings.ToLower(dialect) {
	case "postgres":
		return viper.GetString("database.postgres.dsn")
	case "clickhouse":
		return viper.GetString("database.clickhouse.dsn")
	default:
		return viper.GetString("database.dsn")
	}
}
