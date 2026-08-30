package otel

import (
	"fmt"
	"os"
	"path/filepath"

	otelconfig "github.com/everstacklabs/everstack/cmd/config/otel"
	"github.com/spf13/cobra"
)

// New creates the `otel` subcommand for OTEL collector configuration management
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "otel",
		Short: "Manage OpenTelemetry collector configuration",
		Long: `Manage OpenTelemetry collector configuration for Everstack Gateway.

This command helps you export embedded OTEL collector configurations
that are bundled with the Everstack binary.`,
	}

	cmd.AddCommand(newExportCmd())
	cmd.AddCommand(newRunCmd())

	return cmd
}

func newExportCmd() *cobra.Command {
	var (
		output string
		mode   string
	)

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export embedded OTEL collector configuration",
		Long: `Export the embedded OTEL collector configuration to a file.

Available modes:
  default    - Basic configuration that logs to console (good for development)
  clickhouse - Configuration that exports to ClickHouse (for production)

Examples:
  # Export default config
  mf otel export -o otel-collector-config.yaml

  # Export ClickHouse config
  mf otel export --mode clickhouse -o otel-collector-config.yaml

  # Print to stdout
  mf otel export`,
		RunE: func(cmd *cobra.Command, args []string) error {
			config := otelconfig.GetConfig(mode)

			// If no output file specified, print to stdout
			if output == "" {
				fmt.Fprint(cmd.OutOrStdout(), config)
				return nil
			}

			// Create directory if it doesn't exist
			dir := filepath.Dir(output)
			if dir != "." && dir != "" {
				if err := os.MkdirAll(dir, 0755); err != nil {
					return fmt.Errorf("failed to create directory %s: %w", dir, err)
				}
			}

			// Write to file
			if err := os.WriteFile(output, []byte(config), 0644); err != nil {
				return fmt.Errorf("failed to write config to %s: %w", output, err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "✅ OTEL collector config exported to: %s\n", output)
			fmt.Fprintf(cmd.OutOrStdout(), "\nQuick start:\n")
			fmt.Fprintf(cmd.OutOrStdout(), "  docker run -d \\\n")
			fmt.Fprintf(cmd.OutOrStdout(), "    --name everstack-otel-collector \\\n")
			fmt.Fprintf(cmd.OutOrStdout(), "    --network host \\\n")
			fmt.Fprintf(cmd.OutOrStdout(), "    -v %s:/etc/otel-collector-config.yaml \\\n", output)
			fmt.Fprintf(cmd.OutOrStdout(), "    otel/opentelemetry-collector-contrib:latest \\\n")
			fmt.Fprintf(cmd.OutOrStdout(), "    --config=/etc/otel-collector-config.yaml\n")

			return nil
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "", "output file path (prints to stdout if not specified)")
	cmd.Flags().StringVar(&mode, "mode", "default", "configuration mode: 'default' or 'clickhouse'")

	return cmd
}

func newRunCmd() *cobra.Command {
	var mode string

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Print Docker command to run OTEL collector",
		Long: `Print the Docker command to run the OTEL collector with embedded configuration.

This command outputs a Docker run command that uses the embedded configuration
via process substitution or a temporary file.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()

			fmt.Fprintln(w, "# Run OTEL collector with embedded configuration")
			fmt.Fprintln(w, "")
			fmt.Fprintln(w, "# Option 1: Using temporary file")
			fmt.Fprintf(w, "mf otel export --mode %s -o /tmp/otel-collector-config.yaml\n", mode)
			fmt.Fprintln(w, "docker run -d \\")
			fmt.Fprintln(w, "  --name everstack-otel-collector \\")
			fmt.Fprintln(w, "  --network host \\")
			fmt.Fprintln(w, "  -v /tmp/otel-collector-config.yaml:/etc/otel-collector-config.yaml \\")
			fmt.Fprintln(w, "  otel/opentelemetry-collector-contrib:latest \\")
			fmt.Fprintln(w, "  --config=/etc/otel-collector-config.yaml")
			fmt.Fprintln(w, "")

			if mode == "clickhouse" {
				fmt.Fprintln(w, "# Option 2: With ClickHouse environment variables")
				fmt.Fprintln(w, "docker run -d \\")
				fmt.Fprintln(w, "  --name everstack-otel-collector \\")
				fmt.Fprintln(w, "  --network host \\")
				fmt.Fprintln(w, "  -e CLICKHOUSE_HOST=localhost \\")
				fmt.Fprintln(w, "  -e CLICKHOUSE_PORT=9000 \\")
				fmt.Fprintln(w, "  -e CLICKHOUSE_DATABASE=everstack \\")
				fmt.Fprintln(w, "  -e CLICKHOUSE_USERNAME=clickhouse \\")
				fmt.Fprintln(w, "  -e CLICKHOUSE_PASSWORD=clickhouse \\")
				fmt.Fprintln(w, "  -v /tmp/otel-collector-config.yaml:/etc/otel-collector-config.yaml \\")
				fmt.Fprintln(w, "  otel/opentelemetry-collector-contrib:latest \\")
				fmt.Fprintln(w, "  --config=/etc/otel-collector-config.yaml")
				fmt.Fprintln(w, "")
			}

			fmt.Fprintln(w, "# Then enable telemetry in gateway:")
			fmt.Fprintln(w, "export EVS_TELEMETRY_ENABLED=true")
			fmt.Fprintln(w, "export EVS_TELEMETRY_OTLP_ENDPOINT=\"localhost:4317\"")

			return nil
		},
	}

	cmd.Flags().StringVar(&mode, "mode", "default", "configuration mode: 'default' or 'clickhouse'")

	return cmd
}
