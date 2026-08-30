package cliconfig

import (
	"fmt"
	"os"
	"strings"

	"github.com/everstacklabs/everstack/internal/cli/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// New returns the `evs config` command group.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage CLI configuration and profiles",
	}
	cmd.AddCommand(newSetCmd())
	cmd.AddCommand(newGetCmd())
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newUseContextCmd())
	cmd.AddCommand(newCurrentContextCmd())
	return cmd
}

func newSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a config value in the active profile",
		Example: `  evs config set api_url https://auth.everstack.ai
  evs config set org myorg
  evs config set output json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			ctx := cfg.ActiveCtx()
			if err := applyKey(&ctx, key, value); err != nil {
				return err
			}
			cfg.SetContext(cfg.ActiveContext, ctx)
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "Set %s = %s (context: %s)\n", key, value, cfg.ActiveContext)
			return nil
		},
	}
}

func newGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a config value from the active profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			ctx := cfg.ActiveCtx()
			val, err := readKey(ctx, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, val)
			return nil
		},
	}
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show the active profile configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			resolved := config.Resolve(cfg, "", "", "", "", "")
			out := map[string]interface{}{
				"context":   cfg.ActiveContext,
				"api_url":   resolved.APIURL,
				"org":       resolved.OrgSlug,
				"workspace": resolved.Workspace,
				"output":    resolved.Output,
				"transport": resolved.Transport,
			}
			return yaml.NewEncoder(os.Stdout).Encode(out)
		},
	}
}

func newUseContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use-context <name>",
		Short: "Switch the active profile/context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			// Create the context if it doesn't exist.
			if _, ok := cfg.Contexts[name]; !ok {
				cfg.SetContext(name, config.Context{})
			}
			cfg.ActiveContext = name
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "Switched to context %q\n", name)
			return nil
		},
	}
}

func newCurrentContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "current-context",
		Short: "Show the active context name and summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			resolved := config.Resolve(cfg, "", "", "", "", "")
			fmt.Fprintf(os.Stdout, "Context:   %s\nAPI URL:   %s\nOrg:       %s\nWorkspace: %s\nOutput:    %s\n",
				cfg.ActiveContext, resolved.APIURL, resolved.OrgSlug, resolved.Workspace, resolved.Output)
			return nil
		},
	}
}

func applyKey(ctx *config.Context, key, value string) error {
	switch strings.ToLower(key) {
	case "api_url", "api-url":
		ctx.APIURL = value
	case "org", "org_slug":
		ctx.OrgSlug = value
	case "workspace":
		ctx.Workspace = value
	case "output":
		ctx.Output = value
	case "transport":
		ctx.Transport = value
	default:
		return fmt.Errorf("unknown config key %q; valid: api_url, org, workspace, output, transport", key)
	}
	return nil
}

func readKey(ctx config.Context, key string) (string, error) {
	switch strings.ToLower(key) {
	case "api_url", "api-url":
		return ctx.APIURL, nil
	case "org", "org_slug":
		return ctx.OrgSlug, nil
	case "workspace":
		return ctx.Workspace, nil
	case "output":
		return ctx.Output, nil
	case "transport":
		return ctx.Transport, nil
	default:
		return "", fmt.Errorf("unknown config key %q; valid: api_url, org, workspace, output, transport", key)
	}
}
