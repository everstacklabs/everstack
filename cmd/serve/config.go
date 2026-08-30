package serve

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/common-nighthawk/go-figure"
	"github.com/everstacklabs/everstack/cmd/build"
	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/idgenerator"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/fatih/color"
	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/mux"
	"github.com/jmoiron/sqlx"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	// DefaultConfigPath is the path to the embedded default config within the Docker image
	DefaultConfigPath = "/app/config/default.yaml"
	// EnvConfigURL is the environment variable for specifying a remote config URL
	EnvConfigURL = "EVS_CONFIG_URL"
)

// gatewayReloader is the minimal interface required from the gateway server
// to support hot-reload of gateway and features configuration.
type gatewayReloader interface {
	Reload(gwCfg *validator.GatewayConfig, features *validator.FeaturesConfig)
}

// EmbeddedDefaults holds the embedded default configurations
type EmbeddedDefaults struct {
	Server     []byte
	Models     []byte
	Providers  []byte
	Guardrails []byte
	Alerts     []byte
	Agents     []byte

	// SharedDB is a pre-opened tenant-aware database connection (from pgxpool).
	// When set, startAPI skips database.InitializeFromConfig and uses this
	// connection instead. Used by the cloud control plane to run the gateway
	// API on a shared Postgres instance with per-tenant search_path.
	SharedDB *database.Conn

	// SharedAnalytics is a pre-opened tenant-aware ClickHouse connection.
	// When set alongside SharedDB, startAPI uses hybrid mode with per-tenant
	// ClickHouse database switching via USE {database}.
	SharedAnalytics *sqlx.DB

	// SharedBillingUsageURL is the internal billing-service endpoint used by
	// the managed shared gateway to forward tenant-scoped sandbox meters. It
	// is supplied by the cloud service process rather than inferred from a
	// public URL. Sandbox allocation fails closed when this path is unavailable
	// because sandbox compute is never free.
	SharedBillingUsageURL string

	// AuthProvider overrides the builtin self-hosted auth server.
	// Used by the cloud control plane to inject WorkOS-based authentication.
	// When nil, the builtin self-hosted auth (email/password + magic link) is used.
	AuthProvider AuthProvider

	// EmailCodeSender delivers the evs.run site-claim verification code.
	// Community builds ship no outbound email provider, so this is nil and the
	// hosting server falls back to logging the code; the cloud control plane
	// injects a provider-backed sender. Routing delivery through this slot is
	// what keeps cmd/ free of any import from services/.
	EmailCodeSender func(ctx context.Context, to, code string) error
}

func managedBillingUsageURL(defaults *EmbeddedDefaults) string {
	if defaults != nil {
		if endpoint := strings.TrimSpace(defaults.SharedBillingUsageURL); endpoint != "" {
			return endpoint
		}
	}
	return strings.TrimSpace(os.Getenv("EVS_BILLING_USAGE_URL"))
}

// AuthProvider is an optional interface for injecting an external auth server.
// The cloud control plane implements this to provide WorkOS-based authentication.
type AuthProvider interface {
	RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler)
	RegisterHTTPRoutes(router *mux.Router)
	FileDescriptor() protoreflect.FileDescriptor
	AppName() string
	MethodPrefix() string
}

// resolveConfigPath determines the config source using the following resolution order:
// 1. If --config is specified (via viper) → use that (can be local path or URL)
// 2. If EVS_CONFIG_URL env is set → use that URL
// 3. If /app/config/gateway.yaml exists (mounted volume) → use that
// 4. If /app/config/default.yaml exists (bundled default) → use that
// 5. Otherwise → fatal error
func resolveConfigPath() string {
	// 1. Check if --config flag was provided
	configPath := viper.ConfigFileUsed()
	if configPath != "" {
		logger.Infof("Using config from --config flag: %s", configPath)
		return configPath
	}

	// 2. Check EVS_CONFIG_URL environment variable
	if configURL := os.Getenv(EnvConfigURL); configURL != "" {
		logger.Infof("Using config from %s environment variable", EnvConfigURL)
		return configURL
	}

	// 3. Check for mounted config at standard location
	mountedConfigPath := "/app/config/gateway.yaml"
	if _, err := os.Stat(mountedConfigPath); err == nil {
		logger.Infof("Using mounted config: %s", mountedConfigPath)
		return mountedConfigPath
	}

	// 4. Check for bundled default config
	if _, err := os.Stat(DefaultConfigPath); err == nil {
		logger.Infof("Using bundled default config: %s", DefaultConfigPath)
		return DefaultConfigPath
	}

	// 5. No config found - provide helpful error message
	logger.Fatal(`No configuration found. Please provide a config using one of these methods:

  1. --config flag:     mf serve --config /path/to/gateway.yaml
  2. --config with URL: mf serve --config https://example.com/gateway.yaml
  3. Environment var:   export EVS_CONFIG_URL=https://example.com/gateway.yaml
  4. Mount volume:      docker run -v ./gateway.yaml:/app/config/gateway.yaml ...

For authenticated remote configs, set EVS_CONFIG_AUTH_TOKEN with your bearer token.
`)
	return ""
}

func MustNewConfig(v *viper.Viper, embeddedDefaults *EmbeddedDefaults) *validator.Config {
	// Resolve config path using the resolution order
	configPath := resolveConfigPath()

	// Load user configuration using the new type-safe system
	// LoadConfig now supports both local paths and remote URLs
	userConfig, err := validator.LoadConfig(configPath)
	if err != nil {
		logger.WithFields("error", err, "config_source", configPath).Fatal("failed to load user configuration")
	}

	// Convert embedded defaults to gateway.DefaultConfigs
	defaults := &validator.DefaultConfigs{
		Server:     embeddedDefaults.Server,
		Models:     embeddedDefaults.Models,
		Providers:  embeddedDefaults.Providers,
		Guardrails: embeddedDefaults.Guardrails,
		Alerts:     embeddedDefaults.Alerts,
		Agents:     embeddedDefaults.Agents,
	}

	// Validate default configurations
	if err := validator.ValidateDefaultConfigs(defaults); err != nil {
		logger.WithFields("error", err).Fatal("default configurations validation failed")
	}

	// Load and validate server configuration with defaults
	serverConfig, err := validator.LoadServerConfig(userConfig, defaults)
	if err != nil {
		logger.WithFields("error", err).Fatal("failed to load and validate server configuration")
	}

	// Load and validate gateway configuration
	gatewayConfig, err := validator.LoadGatewayConfig(userConfig, defaults)
	if err != nil {
		logger.WithFields("error", err).Fatal("failed to load and validate gateway configuration")
	}

	config := &validator.Config{
		Server:        serverConfig,
		Gateway:       gatewayConfig,
		SecretManager: userConfig.SecretManager,
		Features:      userConfig.Features,
		Database:      userConfig.Database,
		Cache:         userConfig.Cache, // Load cache configuration from user config
		Auth:          userConfig.Auth,  // Load auth configuration from user config
		// Other components will be loaded as you implement them
	}

	// Run startup validation — logs warnings for common misconfigurations and
	// auto-fixes recoverable issues (e.g. generating a session secret).
	validator.ValidateAndFix(config)

	// Configure logger and ID generator
	if config.Server != nil && config.Server.Log != nil {
		err = config.Server.Log.SetLogger()
		logger.OnError(err).Fatal("unable to set logger")
	}

	if config.Server != nil && config.Server.Machine != nil {
		idgenerator.Configure(config.Server.Machine)
	}

	showConfigSummary(config)

	return config
}

// startConfigWatcher attaches a file watcher on the provided configPath and
// applies validated gateway/features config to the provided reloader with a
// debounce to coalesce writes from editors that save atomically.
func startConfigWatcher(ctx context.Context, configPath string, reloader gatewayReloader) {
	if configPath == "" || reloader == nil {
		return
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	_ = watcher.Add(configPath)
	logger.WithFields("path", configPath).Info("hot-reload watcher active")

	go func() {
		defer watcher.Close()
		var t *time.Timer
		debounce := 800 * time.Millisecond

		fire := func() {
			logger.WithFields("path", configPath).Info("hot-reload: applying config")
			if userCfg, e := validator.LoadConfig(configPath); e == nil {
				defs := &validator.DefaultConfigs{
					Server:     []byte(viper.GetString("defaults.server")),
					Models:     []byte(viper.GetString("defaults.models")),
					Providers:  []byte(viper.GetString("defaults.providers")),
					Guardrails: []byte(viper.GetString("defaults.guardrails")),
					Alerts:     []byte(viper.GetString("defaults.alerts")),
					Agents:     []byte(viper.GetString("defaults.agents")),
					Gateway:    []byte(viper.GetString("defaults.gateway")),
				}
				if gwCfg, e3 := validator.LoadGatewayConfig(userCfg, defs); e3 == nil {
					reloader.Reload(gwCfg, userCfg.Features)
				} else {
					logger.WithError(e3).Warn("hot-reload: gateway config invalid, keeping previous")
				}
			} else {
				logger.WithError(e).Warn("hot-reload: failed reading config, keeping previous")
			}
		}

		for {
			select {
			case ev := <-watcher.Events:
				logger.WithFields("op", ev.Op.String(), "name", ev.Name).Debug("hot-reload: fsnotify event")
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0 {
					if t != nil {
						t.Stop()
					}
					// Re-attach on rename/create to handle editors using atomic save
					if ev.Op&(fsnotify.Rename|fsnotify.Create) != 0 {
						_ = watcher.Remove(configPath)
						_ = watcher.Add(configPath)
					}
					logger.WithFields("debounce_ms", int(debounce/time.Millisecond)).Debug("hot-reload: scheduling apply")
					t = time.AfterFunc(debounce, fire)
				}
			case err := <-watcher.Errors:
				logger.WithError(err).Warn("config watcher error")
			case <-ctx.Done():
				return
			}
		}
	}()
}

// showConfigSummary displays a clean CLI summary of the loaded configuration
func showConfigSummary(config *validator.Config) {
	fmt.Printf("================================================================\n\n")
	fmt.Println(color.BlueString(figure.NewFigure("EVERSTACK", "", true).String()))
	http := "http"
	if config.Server.TLS.Enabled || config.Server.Config.ExternalSecure {
		http = "https"
	}
	// Build host and port with sensible fallbacks if user didn't specify external values
	host := config.Server.Config.ExternalDomain
	if host == "" {
		host = "localhost"
	}
	port := config.Server.Config.ExternalPort
	if port == 0 {
		port = config.Server.Config.Port
		if port == 0 {
			port = 8089
		}
	}
	healthCheckURL := fmt.Sprintf("%s://%s:%v/debug/healthz\n", http, host, port)
	insecure := !config.Server.TLS.Enabled && !config.Server.Config.ExternalSecure
	consoleURL := fmt.Sprintf("%s://%s:%v\n", http, host, port)

	fmt.Printf("═══════════════════════════════════════════════════════════════════════════════\n\n")
	fmt.Printf(" Version          \t: %s\n", build.Version())
	fmt.Printf(" TLS enabled      \t: %v\n", config.Server.TLS.Enabled)
	fmt.Printf(" External Secure \t: %v\n", config.Server.Config.ExternalSecure)
	fmt.Printf(" Console URL     \t: %s", color.BlueString(consoleURL))
	fmt.Printf(" Health Check URL \t: %s", color.BlueString(healthCheckURL))
	if insecure {
		fmt.Printf("\n %s: you're using plain http without TLS. Be aware this is \n", color.RedString("Warning"))
		fmt.Printf(" not a secure setup and should only be used for test systems.         \n")
		fmt.Printf(" Visit: %s    \n", color.CyanString("https://everstack.ai/docs/self-hosting/manage/tls_modes"))
	}
	fmt.Printf("\n═══════════════════════════════════════════════════════════════════════════════\n\n")

	if viper.GetBool("info") {
		// Server Configuration Summary
		fmt.Println("🔧 Server Configuration:")
		if config.Server != nil {
			fmt.Printf("   • Port: %d\n", config.Server.Config.Port)
			fmt.Printf("   • External: %s:%d (secure: %t)\n",
				config.Server.Config.ExternalDomain,
				config.Server.Config.ExternalPort,
				config.Server.Config.ExternalSecure)
			fmt.Printf("   • Metrics: %s (port: %d)\n",
				config.Server.Metrics.Type,
				config.Server.Metrics.Port)
			fmt.Printf("   • Tracing: %s (fraction: %.2f)\n",
				config.Server.Tracing.Type,
				config.Server.Tracing.Fraction)
			fmt.Printf("   • TLS: %t\n", config.Server.TLS.Enabled)
			fmt.Printf("   • CORS: %t\n", config.Server.CORS.Enabled)
		} else {
			fmt.Println("   • Not configured")
		}
		fmt.Println()

		// Gateway Configuration Summary
		fmt.Println("🚪 Gateway Configuration:")
		if config.Gateway != nil {
			fmt.Printf("   • Models: %d configured\n", len(config.Gateway.Models))
			fmt.Printf("   • Rate Limiting: %t\n", config.Gateway.RateLimit.Enabled)
			fmt.Printf("   • Load Balancer: %t\n", config.Gateway.LoadBalancer.Enabled)
			fmt.Printf("   • Memory: %t\n", config.Gateway.Memory.Enabled)
			fmt.Printf("   • Function Calling: %t\n", config.Gateway.Capabilities.FunctionCalling.Enabled)
			fmt.Printf("   • File Processing: %t\n", config.Gateway.FileProcessing.Enabled)
			fmt.Printf("   • Plugins: %t\n", config.Gateway.Plugins.Enabled)
			fmt.Printf("   • Guardrails: %t\n", config.Gateway.Guardrails.Enabled)
			fmt.Printf("   • Agents: %t\n", config.Gateway.Agents.Enabled)

			// Show model details if any
			if len(config.Gateway.Models) > 0 {
				fmt.Println("   • Models:")
				for i, model := range config.Gateway.Models {
					fmt.Printf("     %d. %s (%s) - %d tokens\n",
						i+1,
						strings.Join(model.Model, ","),
						model.Provider,
						model.MaxTokens)
				}
			}
		} else {
			fmt.Println("   • Not configured")
		}
		fmt.Println()

		// Component Status
		fmt.Println("🔌 Component Status:")
		components := []struct {
			name    string
			config  interface{}
			enabled bool
		}{
			{"Secret Manager", config.SecretManager, config.SecretManager != nil},
			{"Database", config.Database, config.Database != nil},
			{"Cache", config.Cache, config.Cache != nil},
			{"Backup", config.Backup, config.Backup != nil},
			{"Alerts", config.Alerts, config.Alerts != nil},
			{"Features", config.Features, config.Features != nil},
		}

		for _, comp := range components {
			status := "❌ Not configured"
			if comp.enabled {
				status = "✅ Configured"
			}
			fmt.Printf("   • %s: %s\n", comp.name, status)
		}
		fmt.Println()

		// Configuration Summary
		fmt.Println("📊 Configuration Summary:")
		totalComponents := 8 // server, gateway, secret_manager, database, cache, backup, alerts, features
		configuredComponents := 0
		if config.Server != nil {
			configuredComponents++
		}
		if config.Gateway != nil {
			configuredComponents++
		}
		if config.SecretManager != nil {
			configuredComponents++
		}
		if config.Database != nil {
			configuredComponents++
		}
		if config.Cache != nil {
			configuredComponents++
		}
		if config.Backup != nil {
			configuredComponents++
		}
		if config.Alerts != nil {
			configuredComponents++
		}
		if config.Features != nil {
			configuredComponents++
		}

		fmt.Printf("   • Components configured: %d/%d\n", configuredComponents, totalComponents)
		fmt.Printf("   • Configuration completeness: %.1f%%\n", float64(configuredComponents)/float64(totalComponents)*100)

		fmt.Printf("\n═══════════════════════════════════════════════════════════════════════════════\n\n")
	}
}

// Legacy function for backward compatibility (if needed)
func MustNewConfigLegacy(v *viper.Viper) *validator.Config {
	config := new(validator.Config)
	err := v.Unmarshal(config, viper.DecodeHook(
		(mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToTimeDurationHookFunc(),
			mapstructure.StringToTimeHookFunc(time.RFC3339),
			mapstructure.StringToSliceHookFunc(","),
			mapstructure.TextUnmarshallerHookFunc(),
		)),
	))

	logger.OnError(err).Fatal("unable to read everstack config")

	if config.Server != nil && config.Server.Log != nil {
		err = config.Server.Log.SetLogger()
		logger.OnError(err).Fatal("unable to set logger")
	}
	if config.Server != nil && config.Server.Machine != nil {
		idgenerator.Configure(config.Server.Machine)
	}

	return config
}
