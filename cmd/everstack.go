package cmd

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"strings"

	agentscmd "github.com/everstacklabs/everstack/cmd/agents"
	"github.com/everstacklabs/everstack/cmd/auth"
	"github.com/everstacklabs/everstack/cmd/backfill"
	"github.com/everstacklabs/everstack/cmd/build"
	cliconfig "github.com/everstacklabs/everstack/cmd/cli_config"
	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/cmd/memory"
	"github.com/everstacklabs/everstack/cmd/migrate"
	"github.com/everstacklabs/everstack/cmd/otel"
	"github.com/everstacklabs/everstack/cmd/sandbox"
	"github.com/everstacklabs/everstack/cmd/serve"
	"github.com/everstacklabs/everstack/cmd/version"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	servicescfg "github.com/everstacklabs/everstack/internal/services/config"
	"github.com/everstacklabs/everstack/internal/updatecheck"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	configFiles   []string
	noUpdateCheck bool

	//go:embed config/gateway/defaults/server.yaml
	serverDefaults []byte

	// Catalog files embedded in binary for production use
	// Source of truth is model-catalog/ (loaded from filesystem in dev, embedded in production)
	// Build process should copy model-catalog/*.yaml to cmd/config/gateway/defaults/ before building
	//go:embed config/gateway/defaults/models.yaml
	modelsDefaults []byte

	// Catalog files embedded in binary for production use
	// Source of truth is model-catalog/ (loaded from filesystem in dev, embedded in production)
	// Build process should copy model-catalog/*.yaml to cmd/config/gateway/defaults/ before building
	//go:embed config/gateway/defaults/providers.yaml
	providersDefaults []byte

	//go:embed config/gateway/defaults/guardrails.yaml
	guardrailsDefaults []byte

	//go:embed config/gateway/defaults/alerts.yaml
	alertsDefaults []byte

	//go:embed config/gateway/defaults/agents.yaml
	agentsDefaults []byte

	// Platform services configuration (license, auth, etc.)
	// This is the single source of truth for cloud service URLs
	// Located at cmd/config/default.yaml
	//go:embed config/default.yaml
	servicesDefaults []byte
)

func New(out io.Writer, in io.Reader, args []string, server chan<- *serve.Server) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "evs",
		Short: "The Everstack CLI lets you interact with Everstack",
		Long:  "The Everstack CLI lets you interact with Everstack",
		// RunE: func(cmd *cobra.Command, args []string) error {
		// 	return errors.New("Welcome to Everstack CLI, {username from zitadel}")
		// },
		Version: build.Version(),
	}
	rootCmd.SetVersionTemplate("{{.Version}}\n")
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	// Ensure flags are parsed before running update check
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		if noUpdateCheck {
			_ = os.Setenv("EVS_NO_UPDATE_CHECK", "1")
		}
		updatecheck.CheckForUpdate(rootCmd.Version)
	}

	viper.AutomaticEnv()
	viper.SetEnvPrefix("EVS")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.SetConfigType("yaml")

	cobra.OnInitialize(initConfig, initDefaults)

	rootCmd.PersistentFlags().StringArrayVar(&configFiles, "config", nil, "path to config file to overwrite system defaults")
	rootCmd.PersistentFlags().BoolVar(&noUpdateCheck, "no-update-check", false, "disable automatic update checks (env: EVS_NO_UPDATE_CHECK)")

	// Set embedded services defaults (security policies, activation, etc.)
	servicescfg.SetEmbeddedDefaults(servicesDefaults)

	// Create embedded defaults from the go:embed data
	embeddedDefaults := &serve.EmbeddedDefaults{
		Server:     serverDefaults,
		Models:     modelsDefaults,
		Providers:  providersDefaults,
		Guardrails: guardrailsDefaults,
		Alerts:     alertsDefaults,
		Agents:     agentsDefaults,
	}

	rootCmd.AddCommand(serve.New(server, embeddedDefaults))
	rootCmd.AddCommand(migrate.New())
	rootCmd.AddCommand(backfill.New())
	rootCmd.AddCommand(backfill.NewStorageCredentialBackfillCommand())
	rootCmd.AddCommand(backfill.NewManagedStorageDefaultBackfillCommand())
	rootCmd.AddCommand(version.New())
	rootCmd.AddCommand(otel.New())
	rootCmd.AddCommand(memory.New())
	rootCmd.AddCommand(sandbox.New())
	rootCmd.AddCommand(agentscmd.New())
	rootCmd.AddCommand(agentscmd.NewInit())
	rootCmd.AddCommand(agentscmd.NewDeploy())

	// User-facing cloud CLI commands.
	authCmd := auth.New()
	rootCmd.AddCommand(authCmd)
	rootCmd.AddCommand(cliconfig.New())

	// Top-level aliases so users can type `evs login` / `evs whoami` / `evs logout`
	// without the `auth` prefix.
	for _, sub := range authCmd.Commands() {
		alias := *sub
		alias.Hidden = false
		rootCmd.AddCommand(&alias)
	}

	registerInternalCommands(rootCmd)
	return rootCmd
}

func initConfig() {
	for _, f := range configFiles {
		viper.SetConfigFile(f)
		err := viper.MergeInConfig()
		logger.WithFields("file", f).OnError(err).Warn("Unable to read config file")
	}
}

func initDefaults() {
	// Load default configurations using go:embed
	defaults, err := loadEmbeddedDefaults()
	if err != nil {
		logger.WithFields("error", err).Fatal("Failed to load embedded defaults")
	}

	// Validate the loaded configurations
	if err := validator.ValidateDefaultConfigs(defaults); err != nil {
		logger.WithFields("error", err).Warn("Default configurations validation failed")
	}

	// Merge the loaded defaults with viper
	mergeDefaultsWithViper(defaults)
}

// loadEmbeddedDefaults loads default configurations using go:embed
func loadEmbeddedDefaults() (*validator.DefaultConfigs, error) {
	defaults := &validator.DefaultConfigs{}

	// Load server defaults
	if len(serverDefaults) > 0 {
		defaults.Server = serverDefaults
	} else {
		return nil, fmt.Errorf("server defaults not found")
	}

	// Load models defaults
	if len(modelsDefaults) > 0 {
		defaults.Models = modelsDefaults
	} else {
		return nil, fmt.Errorf("models defaults not found")
	}

	// Load providers defaults
	if len(providersDefaults) > 0 {
		defaults.Providers = providersDefaults
	} else {
		return nil, fmt.Errorf("providers defaults not found")
	}

	// Load guardrails defaults
	if len(guardrailsDefaults) > 0 {
		defaults.Guardrails = guardrailsDefaults
	} else {
		return nil, fmt.Errorf("guardrails defaults not found")
	}

	// Load alerts defaults
	if len(alertsDefaults) > 0 {
		defaults.Alerts = alertsDefaults
	} else {
		return nil, fmt.Errorf("alerts defaults not found")
	}

	// Load agents defaults (optional)
	if len(agentsDefaults) > 0 {
		defaults.Agents = agentsDefaults
	} else {
		fmt.Printf("Warning: agents defaults not found, skipping\n")
	}

	return defaults, nil
}

// mergeDefaultsWithViper merges the loaded default configurations with viper
func mergeDefaultsWithViper(defaults *validator.DefaultConfigs) {
	// For now, we'll just store the defaults in viper as raw data
	// When you implement each component, you can load them individually

	// Store server defaults
	if len(defaults.Server) > 0 {
		viper.Set("defaults.server", string(defaults.Server))
	}

	// Store models defaults
	if len(defaults.Models) > 0 {
		viper.Set("defaults.models", string(defaults.Models))
	}

	// Store providers defaults
	if len(defaults.Providers) > 0 {
		viper.Set("defaults.providers", string(defaults.Providers))
	}

	// Store guardrails defaults
	if len(defaults.Guardrails) > 0 {
		viper.Set("defaults.guardrails", string(defaults.Guardrails))
	}

	// Store alerts defaults
	if len(defaults.Alerts) > 0 {
		viper.Set("defaults.alerts", string(defaults.Alerts))
	}

	// Store agents defaults
	if len(defaults.Agents) > 0 {
		viper.Set("defaults.agents", string(defaults.Agents))
	}
}
