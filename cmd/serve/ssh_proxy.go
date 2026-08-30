package serve

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	sshpkg "github.com/everstacklabs/everstack/internal/ssh"
)

const (
	defaultSSHProxyPostgresMaxOpenConns = 5
	defaultSSHProxyPostgresMaxIdleConns = 2
)

func newSSHProxyCommand(embeddedDefaults *EmbeddedDefaults) *cobra.Command {
	return &cobra.Command{
		Use:    "ssh-proxy",
		Short:  "Run the internal sandbox SSH proxy",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			config := MustNewConfig(viper.GetViper(), embeddedDefaults)
			return startSSHProxyOnly(cmd.Context(), config, embeddedDefaults)
		},
	}
}

func startSSHProxyOnly(parent context.Context, config *validator.Config, embeddedDefaults *EmbeddedDefaults) error {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, closeDB, err := openSSHProxyPostgres(ctx, config)
	if err != nil {
		return err
	}
	defer closeDB()

	var sandboxCfg *validator.SandboxFeaturesConfig
	if config != nil && config.Features != nil {
		sandboxCfg = &config.Features.Sandbox
	}
	sharedMode := embeddedDefaults != nil && embeddedDefaults.SharedDB != nil
	sandboxMgr := initSandboxManager(sandboxCfg, sharedMode)
	if sandboxMgr == nil {
		return fmt.Errorf("ssh-proxy: sandbox manager is not available")
	}
	sandboxMgr.SetDB(db)

	sshCfg := resolveSSHRuntimeConfig(sandboxCfg)
	if sshCfg.ListenAddr == "disabled" {
		return fmt.Errorf("ssh-proxy: EVS_SSH_LISTEN_ADDR is disabled")
	}
	hostKeySigner, err := loadSSHHostKeySigner(true)
	if err != nil {
		return fmt.Errorf("ssh-proxy: load host key: %w", err)
	}

	proxy := sshpkg.NewProxy(sshpkg.ProxyConfig{
		ListenAddr:     sshCfg.ListenAddr,
		HostKeySigner:  hostKeySigner,
		KeyStore:       sshpkg.NewKeyStore(db),
		SandboxManager: sandboxMgr,
	})
	if err := proxy.Start(); err != nil {
		return err
	}
	logger.WithFields("addr", sshCfg.ListenAddr, "host", sshCfg.Host, "port", sshCfg.PublicPort, "fingerprint", proxy.Fingerprint()).
		Info("ssh-proxy: internal SSH proxy started")

	<-ctx.Done()
	proxy.Stop()
	return nil
}

func openSSHProxyPostgres(ctx context.Context, config *validator.Config) (*sqlx.DB, func(), error) {
	dsn := strings.TrimSpace(os.Getenv(database.EnvPostgresDSN))
	if dsn == "" && config != nil && config.Database != nil {
		dsn = strings.TrimSpace(config.Database.Postgres.DSN)
	}
	if dsn == "" {
		return nil, nil, fmt.Errorf("ssh-proxy: Postgres DSN is required via database.postgres.dsn or %s", database.EnvPostgresDSN)
	}

	conn, err := database.Open(ctx, database.Config{
		Type:    database.TypePostgres,
		DSN:     dsn,
		MaxOpen: getEnvIntOrDefault("EVS_SSH_PROXY_POSTGRES_MAX_OPEN_CONNS", defaultSSHProxyPostgresMaxOpenConns),
		MaxIdle: getEnvIntOrDefault("EVS_SSH_PROXY_POSTGRES_MAX_IDLE_CONNS", defaultSSHProxyPostgresMaxIdleConns),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("ssh-proxy: open postgres: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := conn.RW.PingContext(pingCtx); err != nil {
		_ = conn.Close(context.Background())
		return nil, nil, fmt.Errorf("ssh-proxy: postgres ping failed: %w", err)
	}
	return conn.RW, func() { _ = conn.Close(context.Background()) }, nil
}
