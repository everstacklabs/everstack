package serve

import (
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var (
	startInstanceFlagSet = &pflag.FlagSet{}
)

func init() {
	// Public flags (visible in --help)
	startInstanceFlagSet.Uint16("port", 8089, "Port to listen on")
	startInstanceFlagSet.String("customDomain", "", "Custom domain to use for the Everstack instance (e.g. https://mydomain.com)")
	startInstanceFlagSet.String("customPort", "", "Custom port to listen on (e.g. 8080)")
	startInstanceFlagSet.Bool("info", false, "Show information about the Everstack instance")

	// Internal-only flags (hidden from --help, but still accessible via CLI and viper)
	startInstanceFlagSet.Bool("validate-config", false, "Validate configuration against schemas and exit")
	startInstanceFlagSet.Bool("validate-on-start", false, "Validate configuration before starting; log result")
	startInstanceFlagSet.Bool("validate-strict", false, "With --validate-on-start, fail startup if invalid")
	startInstanceFlagSet.Bool("pprof", false, "Enable pprof profiling endpoints on /debug/pprof")
	startInstanceFlagSet.Bool("fastpath", false, "Enable fast-path metrics endpoints on /metrics/fastpath and /debug/fastpath")

	// Hide internal flags from help output
	_ = startInstanceFlagSet.MarkHidden("validate-config")
	_ = startInstanceFlagSet.MarkHidden("validate-on-start")
	_ = startInstanceFlagSet.MarkHidden("validate-strict")
	_ = startInstanceFlagSet.MarkHidden("pprof")
	_ = startInstanceFlagSet.MarkHidden("fastpath")
}

func StartFlags(cmd *cobra.Command) {
	cmd.Flags().AddFlagSet(startInstanceFlagSet)
	logger.OnError(viper.BindPFlags(startInstanceFlagSet)).Fatal("could not bind flags")

	// tls.AddTLSModeFlag(cmd)
	// license.LicenseKeyFlag(cmd)
}
