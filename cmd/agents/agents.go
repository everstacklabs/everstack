// Package agents implements the directory-based agent framework CLI:
// `evs init` scaffolds a project, `evs deploy` compiles it onto the
// platform (agent definition + isolated function tools + inline skills +
// triggers), `evs agents run` chats with a deployed agent, and
// `evs agents pull` exports a platform agent back into the directory
// format. See docs/design/agent-sites-extension-plan.md.
package agents

import (
	"context"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/cli/client"
	cliruntime "github.com/everstacklabs/everstack/internal/cli/runtime"
	agentsv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/spf13/cobra"
)

type connectionOptions struct {
	apiURL   string
	apiKey   string
	tenantID string
}

// New creates the `evs agents` command group.
func New() *cobra.Command {
	options := &connectionOptions{}
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Run and sync directory-based agents",
		Long: `Work with agents deployed from directory projects.

An agent project is a folder with agent.yaml, instructions.md, tools/ and
skills/. Deploy it with ` + "`evs deploy`" + `; it becomes a regular platform agent
(visible in the dashboard, running on the platform agent runtime).`,
	}
	addConnectionFlags(cmd, true, options)

	cmd.AddCommand(newRunCmd(options))
	cmd.AddCommand(newPullCmd(options))

	for _, sub := range cmd.Commands() {
		sub.SilenceUsage = true
		sub.SilenceErrors = true
	}
	return cmd
}

func addConnectionFlags(cmd *cobra.Command, persistent bool, options *connectionOptions) {
	flags := cmd.Flags()
	if persistent {
		flags = cmd.PersistentFlags()
	}
	flags.StringVar(&options.apiURL, "api-url", "", "API base URL (env: EVS_API_URL; default: active context)")
	flags.StringVar(&options.apiKey, "api-key", "", "API key override (env: EVS_API_KEY; default: active login)")
	flags.StringVar(&options.tenantID, "tenant-id", "", "tenant ID override (env: EVS_TENANT_ID)")
}

// NewInit exposes `evs init` at the top level.
func NewInit() *cobra.Command {
	c := newInitCmd()
	c.SilenceUsage = true
	c.SilenceErrors = true
	return c
}

// NewDeploy exposes `evs deploy` at the top level.
func NewDeploy() *cobra.Command {
	c := newDeployCmd(&connectionOptions{})
	c.SilenceUsage = true
	c.SilenceErrors = true
	return c
}

// requireFactory builds an authenticated client factory. Unlike `evs sites`,
// every command here needs credentials.
func requireFactory(options connectionOptions) (*client.Factory, error) {
	resolved, err := cliruntime.Resolve(cliruntime.Overrides{
		APIURL:   options.apiURL,
		APIKey:   options.apiKey,
		TenantID: options.tenantID,
	})
	if err != nil {
		return nil, err
	}
	return client.New(client.Options{
		APIURL:            resolved.APIURL,
		AccessToken:       resolved.AccessToken,
		AccessTokenSource: resolved.AccessTokenSource,
		APIKey:            resolved.APIKey,
		OrgID:             resolved.OrgID,
		TenantID:          resolved.TenantID,
	}), nil
}

// findAgentByName lists agents (including hidden) and returns the one whose
// name matches exactly, or nil.
func findAgentByName(ctx context.Context, f *client.Factory, name string) (*agentsv1.AgentDefinition, error) {
	limit := int32(500)
	for offset := int32(0); ; offset += limit {
		resp, err := f.Agents().ListAgents(ctx, connect.NewRequest(&agentsv1.ListAgentsRequest{
			Limit:         &limit,
			Offset:        &offset,
			IncludeHidden: true,
		}))
		if err != nil {
			return nil, client.MapError(err)
		}
		page := resp.Msg.GetAgents()
		for _, a := range page {
			if a.GetName() == name {
				return a, nil
			}
		}
		if len(page) < int(limit) {
			return nil, nil
		}
	}
}

func taskModeToProto(mode string) agentsv1.TaskPermissionMode {
	switch mode {
	case "always":
		return agentsv1.TaskPermissionMode_TASK_PERMISSION_MODE_ALWAYS
	case "deny":
		return agentsv1.TaskPermissionMode_TASK_PERMISSION_MODE_DENY
	case "ask":
		return agentsv1.TaskPermissionMode_TASK_PERMISSION_MODE_ASK
	default:
		return agentsv1.TaskPermissionMode_TASK_PERMISSION_MODE_UNSPECIFIED
	}
}

func taskModeFromProto(mode agentsv1.TaskPermissionMode) string {
	switch mode {
	case agentsv1.TaskPermissionMode_TASK_PERMISSION_MODE_ALWAYS:
		return "always"
	case agentsv1.TaskPermissionMode_TASK_PERMISSION_MODE_DENY:
		return "deny"
	case agentsv1.TaskPermissionMode_TASK_PERMISSION_MODE_ASK:
		return "ask"
	default:
		return ""
	}
}
