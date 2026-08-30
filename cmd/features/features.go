package features

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var apiURL string

func newClient() *Client {
	return NewClient(apiURL)
}

// New creates the `features` subcommand for managing feature flags.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "features",
		Short: "Manage feature flags (internal tool)",
		Long:  "Internal tool for managing feature definitions, per-tenant overrides, and manifest publishing.",
	}

	cmd.PersistentFlags().StringVar(&apiURL, "api-url", envOrDefault("EVS_FEATURES_API_URL", "http://localhost:8089"), "license service base URL")

	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newGetCmd())
	cmd.AddCommand(newCreateCmd())
	cmd.AddCommand(newUpdateCmd())
	cmd.AddCommand(newDeleteCmd())
	cmd.AddCommand(newOverridesCmd())
	cmd.AddCommand(newPublishCmd())

	return cmd
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ── list ────────────────────────────────────────────────────────────

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all feature definitions",
		RunE: func(cmd *cobra.Command, args []string) error {
			features, err := newClient().ListFeatures()
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "KEY\tNAME\tSTATUS\tENABLED\tMIN TIER\tCATEGORIES")
			for _, f := range features {
				tier := "-"
				if f.MinTier != nil {
					tier = *f.MinTier
				}
				cats := "-"
				if len(f.Categories) > 0 {
					cats = strings.Join(f.Categories, ",")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%v\t%s\t%s\n",
					f.Key, f.Name, f.Status, f.Enabled, tier, cats)
			}
			return w.Flush()
		},
	}
}

// ── get ─────────────────────────────────────────────────────────────

func newGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a feature definition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := newClient().GetFeature(args[0])
			if err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Key:         %s\n", f.Key)
			fmt.Fprintf(w, "Name:        %s\n", f.Name)
			fmt.Fprintf(w, "Description: %s\n", f.Description)
			fmt.Fprintf(w, "Status:      %s\n", f.Status)
			fmt.Fprintf(w, "Enabled:     %v\n", f.Enabled)
			tier := "-"
			if f.MinTier != nil {
				tier = *f.MinTier
			}
			fmt.Fprintf(w, "Min Tier:    %s\n", tier)
			cats := "-"
			if len(f.Categories) > 0 {
				cats = strings.Join(f.Categories, ", ")
			}
			fmt.Fprintf(w, "Categories:  %s\n", cats)
			fmt.Fprintf(w, "Created:     %s\n", f.CreatedAt)
			fmt.Fprintf(w, "Updated:     %s\n", f.UpdatedAt)
			return nil
		},
	}
}

// ── create ──────────────────────────────────────────────────────────

func newCreateCmd() *cobra.Command {
	var (
		name        string
		description string
		status      string
		enabled     bool
		minTier     string
		categories  []string
	)

	cmd := &cobra.Command{
		Use:   "create <key>",
		Short: "Create a feature definition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			def := &FeatureDefinition{
				Key:         args[0],
				Name:        name,
				Description: description,
				Status:      status,
				Enabled:     enabled,
				Categories:  categories,
			}
			if minTier != "" {
				def.MinTier = &minTier
			}

			f, err := newClient().CreateFeature(def)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created feature %q (status=%s, enabled=%v)\n", f.Key, f.Status, f.Enabled)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "display name (required)")
	cmd.Flags().StringVar(&description, "description", "", "description")
	cmd.Flags().StringVar(&status, "status", "development", "status: development, beta, released, deprecated")
	cmd.Flags().BoolVar(&enabled, "enabled", false, "enable the feature")
	cmd.Flags().StringVar(&minTier, "min-tier", "", "minimum tier: free, basic, pro, enterprise")
	cmd.Flags().StringSliceVar(&categories, "categories", nil, "categories (comma-separated)")
	_ = cmd.MarkFlagRequired("name")

	return cmd
}

// ── update ──────────────────────────────────────────────────────────

func newUpdateCmd() *cobra.Command {
	var (
		name        string
		description string
		status      string
		enabled     *bool
		minTier     string
		categories  []string
	)

	cmd := &cobra.Command{
		Use:   "update <key>",
		Short: "Update a feature definition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			def := &FeatureDefinition{
				Name:        name,
				Description: description,
				Status:      status,
				Categories:  categories,
			}
			if enabled != nil {
				def.Enabled = *enabled
			}
			if minTier != "" {
				def.MinTier = &minTier
			}

			f, err := newClient().UpdateFeature(args[0], def)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated feature %q (status=%s, enabled=%v)\n", f.Key, f.Status, f.Enabled)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "display name")
	cmd.Flags().StringVar(&description, "description", "", "description")
	cmd.Flags().StringVar(&status, "status", "", "status: development, beta, released, deprecated")
	enabledVal := false
	cmd.Flags().BoolVar(&enabledVal, "enabled", false, "enable the feature")
	cmd.Flags().StringVar(&minTier, "min-tier", "", "minimum tier")
	cmd.Flags().StringSliceVar(&categories, "categories", nil, "categories")

	// Track whether --enabled was explicitly set
	originalPreRun := cmd.PreRunE
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("enabled") {
			enabled = &enabledVal
		}
		if originalPreRun != nil {
			return originalPreRun(cmd, args)
		}
		return nil
	}

	return cmd
}

// ── delete ──────────────────────────────────────────────────────────

func newDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <key>",
		Short: "Delete a feature definition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := newClient().DeleteFeature(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted feature %q\n", args[0])
			return nil
		},
	}
}

// ── publish ─────────────────────────────────────────────────────────

func newPublishCmd() *cobra.Command {
	var tenantID string

	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Force-publish manifest to Cloudflare KV",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := newClient().Publish(tenantID); err != nil {
				return err
			}
			msg := "Published global manifest"
			if tenantID != "" {
				msg += fmt.Sprintf(" + tenant overlay for %s", tenantID)
			}
			fmt.Fprintln(cmd.OutOrStdout(), msg)
			return nil
		},
	}

	cmd.Flags().StringVar(&tenantID, "tenant-id", "", "also publish tenant overlay")
	return cmd
}

// ── overrides ───────────────────────────────────────────────────────

func newOverridesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "overrides",
		Short: "Manage per-tenant feature overrides",
	}

	cmd.AddCommand(newOverridesListCmd())
	cmd.AddCommand(newOverridesSetCmd())
	cmd.AddCommand(newOverridesDeleteCmd())

	return cmd
}

func newOverridesListCmd() *cobra.Command {
	var tenantID string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List overrides for a tenant",
		RunE: func(cmd *cobra.Command, args []string) error {
			overrides, err := newClient().ListOverrides(tenantID)
			if err != nil {
				return err
			}

			if len(overrides) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "No overrides for tenant %s\n", tenantID)
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "FEATURE KEY\tENABLED\tEXPIRES AT\tREASON\tCREATED BY")
			for _, o := range overrides {
				expires := "-"
				if o.ExpiresAt != nil {
					expires = *o.ExpiresAt
				}
				reason := "-"
				if o.Reason != nil {
					reason = *o.Reason
				}
				createdBy := "-"
				if o.CreatedBy != nil {
					createdBy = *o.CreatedBy
				}
				fmt.Fprintf(w, "%s\t%v\t%s\t%s\t%s\n",
					o.FeatureKey, o.Enabled, expires, reason, createdBy)
			}
			return w.Flush()
		},
	}

	cmd.Flags().StringVar(&tenantID, "tenant-id", "", "tenant ID (required)")
	_ = cmd.MarkFlagRequired("tenant-id")
	return cmd
}

func newOverridesSetCmd() *cobra.Command {
	var (
		tenantID   string
		featureKey string
		enabled    bool
		expiresAt  string
		reason     string
		createdBy  string
	)

	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set a per-tenant feature override",
		RunE: func(cmd *cobra.Command, args []string) error {
			override := &FeatureOverride{
				FeatureKey: featureKey,
				Enabled:    enabled,
			}
			if expiresAt != "" {
				override.ExpiresAt = &expiresAt
			}
			if reason != "" {
				override.Reason = &reason
			}
			if createdBy != "" {
				override.CreatedBy = &createdBy
			}

			if err := newClient().SetOverride(tenantID, override); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Set override: tenant=%s feature=%s enabled=%v\n", tenantID, featureKey, enabled)
			return nil
		},
	}

	cmd.Flags().StringVar(&tenantID, "tenant-id", "", "tenant ID (required)")
	cmd.Flags().StringVar(&featureKey, "feature-key", "", "feature key (required)")
	cmd.Flags().BoolVar(&enabled, "enabled", true, "enable or disable")
	cmd.Flags().StringVar(&expiresAt, "expires-at", "", "expiry time (RFC3339)")
	cmd.Flags().StringVar(&reason, "reason", "", "reason for override")
	cmd.Flags().StringVar(&createdBy, "created-by", "", "who created this override")
	_ = cmd.MarkFlagRequired("tenant-id")
	_ = cmd.MarkFlagRequired("feature-key")

	return cmd
}

func newOverridesDeleteCmd() *cobra.Command {
	var tenantID string

	cmd := &cobra.Command{
		Use:   "delete <feature-key>",
		Short: "Delete a per-tenant feature override",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := newClient().DeleteOverride(tenantID, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted override: tenant=%s feature=%s\n", tenantID, args[0])
			return nil
		},
	}

	cmd.Flags().StringVar(&tenantID, "tenant-id", "", "tenant ID (required)")
	_ = cmd.MarkFlagRequired("tenant-id")
	return cmd
}
