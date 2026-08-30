package sandbox

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"
	"time"

	cliclient "github.com/everstacklabs/everstack/internal/cli/client"
	cliruntime "github.com/everstacklabs/everstack/internal/cli/runtime"
	"github.com/spf13/cobra"
)

var (
	apiURL         string
	apiKey         string
	tenantID       string
	requestTimeout time.Duration
	outputJSON     bool
)

// New creates the `sandbox` subcommand for sandbox management.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Manage sandboxes from the CLI",
		Long:  "Manage sandbox instances, command execution, networking, and SSH access from the Everstack CLI.",
	}

	cmd.PersistentFlags().StringVar(&apiURL, "api-url", "", "API server base URL (env: EVS_API_URL; default: active context)")
	cmd.PersistentFlags().StringVar(&apiKey, "api-key", "", "API key override (env: EVS_API_KEY; default: active login)")
	cmd.PersistentFlags().StringVar(&tenantID, "tenant-id", "", "tenant ID override (env: EVS_TENANT_ID)")
	cmd.PersistentFlags().DurationVar(&requestTimeout, "timeout", 30*time.Second, "request timeout")
	cmd.PersistentFlags().BoolVar(&outputJSON, "json", false, "output JSON")

	cmd.AddCommand(newCreateCmd())
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newGetCmd())
	cmd.AddCommand(newOverviewCmd())
	cmd.AddCommand(newExecCmd())
	cmd.AddCommand(newShellCmd())
	cmd.AddCommand(newLogsCmd())
	cmd.AddCommand(newStatsCmd())
	cmd.AddCommand(newEventsCmd())
	cmd.AddCommand(newDestroyCmd())
	cmd.AddCommand(newStopCmd())
	cmd.AddCommand(newReviveCmd())
	cmd.AddCommand(newTerminateCmd())
	cmd.AddCommand(newRecreateCmd())
	cmd.AddCommand(newPortsCmd())
	cmd.AddCommand(newSSHInfoCmd())
	cmd.AddCommand(newSSHCmd())
	cmd.AddCommand(newSSHKeysCmd())

	return cmd
}

func newClient() *Client {
	resolved, err := resolveSession()
	client := NewClient(resolved.APIURL, resolved.APIKey, resolved.TenantID, requestTimeout)
	client.initErr = err
	if err == nil {
		factory := cliclient.New(cliclient.Options{
			APIURL:            resolved.APIURL,
			AccessToken:       resolved.AccessToken,
			AccessTokenSource: resolved.AccessTokenSource,
			APIKey:            resolved.APIKey,
			OrgID:             resolved.OrgID,
			TenantID:          resolved.TenantID,
			Timeout:           requestTimeout,
		})
		client.httpClient = factory.HTTPClient()
	}
	return client
}

func resolveSession() (cliruntime.Session, error) {
	return cliruntime.Resolve(cliruntime.Overrides{
		APIURL:   apiURL,
		APIKey:   apiKey,
		TenantID: tenantID,
	})
}

func writeJSON(cmd *cobra.Command, v interface{}) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printKV(cmd *cobra.Command, rows map[string]string) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
	for k, v := range rows {
		fmt.Fprintf(w, "%s\t%s\n", k, v)
	}
	_ = w.Flush()
}

func requireScaffoldScope() error {
	resolved, err := resolveSession()
	if err != nil {
		return err
	}
	// Device/OAuth sessions are scoped to the instance selected by the verified
	// request host. Sending the login organization as tenant_id would select the
	// wrong resource boundary. API-key calls do not carry that host binding, so
	// they still require an explicit instance tenant.
	if resolved.TenantID == "" && resolved.AccessToken == "" {
		return fmt.Errorf("--tenant-id (or EVS_TENANT_ID) is required for this command")
	}
	return nil
}
