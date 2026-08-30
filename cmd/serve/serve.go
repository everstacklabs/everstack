package serve

import (
	"crypto/tls"
	"fmt"
	"os"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	cfgsvc "github.com/everstacklabs/everstack/internal/config"
	"github.com/gorilla/mux"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func New(server chan<- *Server, embeddedDefaults *EmbeddedDefaults) *cobra.Command {
	serve := &cobra.Command{
		Use:   "serve",
		Short: "Serves Everstack server instance",
		Long: `Serves the Everstack server instance. ` + "\n\n" +
			`Requirements: ` + "\n" +
			`- postgresql database is running. ` + "\n" +
			`- config is provided and properly validated. ` + "\n" +
			``,
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: implement tls mode for start command
			// err := cmd_tls.ModeFromFlag(cmd)
			// if err != nil {
			// 	return err
			// }
			// Run preflight validation if requested; exit early on --validate-config
			if err := validateConfig(cmd); err != nil {
				return err
			}

			config := MustNewConfig(viper.GetViper(), embeddedDefaults)
			cfgPath := viper.ConfigFileUsed()
			// instanceLicenseKey, err := license.LicenseKey(cmd)
			// if err != nil {
			// 	return err
			// }
			return startGatewayServer(cmd.Context(), config, cfgPath, embeddedDefaults, server)
		},
	}

	StartFlags(serve)
	serve.AddCommand(newSSHProxyCommand(embeddedDefaults))

	return serve
}

type Server struct {
	Config    *validator.Config
	Router    *mux.Router
	TLSConfig *tls.Config
	Shutdown  chan<- os.Signal
}

func validateConfig(cmd *cobra.Command) error {

	// Optional preflight validation using JSON Schemas
	validateOnly := viper.GetBool("validate-config")
	validateOnStart := viper.GetBool("validate-on-start")
	validateStrict := viper.GetBool("validate-strict")

	if validateOnly || validateOnStart {
		svc := cfgsvc.NewService()
		schemaFiles := map[string]string{
			"config": "cmd/config/gateway/schemas/config.json",
			"models": "cmd/config/gateway/schemas/models.json",
		}
		if err := svc.LoadSchemasFromFiles(schemaFiles); err != nil {
			return err
		}
		var merged map[string]any
		if err := viper.Unmarshal(&merged); err != nil {
			return err
		}
		result, err := svc.ValidateMap(merged)
		if err != nil {
			return err
		}
		if result != nil && !result.Valid {
			max := 5
			if len(result.Errors) < max {
				max = len(result.Errors)
			}
			for i := 0; i < max; i++ {
				e := result.Errors[i]
				cmd.Printf("config validation error: %s - %s (%s)\n", e.Field, e.Message, e.Severity.String())
			}
			if validateOnly || validateStrict {
				return fmt.Errorf("configuration invalid: %d error(s)", len(result.Errors))
			}
		} else {
			cmd.Println("configuration validation passed")
		}
		if validateOnly {
			return nil
		}
	}
	return nil
}
