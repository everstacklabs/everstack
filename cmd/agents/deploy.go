package agents

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/cli/agentproject"
	"github.com/everstacklabs/everstack/internal/cli/client"
	agentsv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/spf13/cobra"
)

// projectMetaKey is the agent-config key `evs deploy` uses to stamp the
// deployed project state, enabling drift detection on the next deploy.
const projectMetaKey = "agentproject"

// agentLinkTargetAgent is the AgentLink target_type used when both ends of
// the link are agent definitions.
const agentLinkTargetAgent = "agent"

func newDeployCmd(options *connectionOptions) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "deploy [dir]",
		Short: "Deploy an agent project directory to the platform",
		Long: `Deploy a directory-based agent project (agent.yaml, instructions.md,
tools/, skills/, subagents/) to the platform.

Deploy is an idempotent sync keyed by the agent name. Project files and
callable TypeScript, JavaScript, or Python exports become an immutable agent
revision, skills become inline skills, triggers are synced, and every new
session is pinned to the revision it starts with. An exact redeploy is a
no-op. Changing any existing managed state requires --force; use
` + "`evs agents pull`" + ` first when dashboard state should be preserved.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runDeploy(cmd, dir, force, *options)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "converge existing agent resources to this project, overwriting conflicting state")
	addConnectionFlags(cmd, false, options)
	return cmd
}

func runDeploy(cmd *cobra.Command, dir string, force bool, options connectionOptions) error {
	out := cmd.OutOrStdout()
	ctx := cmd.Context()

	p, err := agentproject.Load(dir)
	if err != nil {
		return err
	}
	f, err := requireFactory(options)
	if err != nil {
		return err
	}
	if err := deployGraph(ctx, f, out, p, force); err != nil {
		return err
	}

	if len(p.Config.Channels) > 0 {
		fmt.Fprintln(out, "  channels: not synced by the CLI beta yet; bind channels in the dashboard (Deployments > Channels)")
	}

	fmt.Fprintf(out, "\nDeployed %s. Try it:\n  evs agents run %s \"hello\"\n", p.Config.Name, p.Config.Name)
	return nil
}

// checkDrift validates the structural deployment stamp. Exact desired-state
// comparison is performed separately, so the stamp alone never authorizes an
// overwrite. A missing stamp means the name collides with unmanaged state.
func checkDrift(existing *agentsv1.AgentDefinition, name string) error {
	meta := existing.GetConfig().GetFields()[projectMetaKey]
	if meta == nil {
		return fmt.Errorf("an agent named %q already exists but was not created by `evs deploy`; pass --force to take it over, or rename this project", name)
	}
	metaStruct := meta.GetStructValue()
	if metaStruct == nil {
		return invalidDeploymentStampError(name)
	}
	fields := metaStruct.GetFields()
	if fields["source"].GetStringValue() != "evs deploy" || fields["hash"].GetStringValue() == "" {
		return invalidDeploymentStampError(name)
	}
	if fields["format"].GetNumberValue() != deploymentStampFormat {
		return invalidDeploymentStampError(name)
	}
	if _, err := time.Parse(time.RFC3339, fields["deployed_at"].GetStringValue()); err != nil {
		return invalidDeploymentStampError(name)
	}
	manifest := fields["managed_triggers"].GetListValue()
	if manifest == nil {
		return invalidDeploymentStampError(name)
	}
	seenIDs := map[string]struct{}{}
	seenNames := map[string]struct{}{}
	for _, entry := range manifest.GetValues() {
		record := entry.GetStructValue()
		id := record.GetFields()["id"].GetStringValue()
		triggerName := record.GetFields()["name"].GetStringValue()
		if id == "" || triggerName == "" {
			return invalidDeploymentStampError(name)
		}
		if _, duplicate := seenIDs[id]; duplicate {
			return invalidDeploymentStampError(name)
		}
		if _, duplicate := seenNames[triggerName]; duplicate {
			return invalidDeploymentStampError(name)
		}
		seenIDs[id] = struct{}{}
		seenNames[triggerName] = struct{}{}
	}
	return nil
}

func invalidDeploymentStampError(name string) error {
	return fmt.Errorf("agent %q has an invalid deployment stamp; run `evs agents pull %s` to inspect it, or pass --force to overwrite", name, name)
}

func syncTriggers(
	ctx context.Context,
	f *client.Factory,
	cmd *cobra.Command,
	agentID string,
	p *agentproject.Project,
	previousManaged map[string]string,
	force bool,
) error {
	out := cmd.OutOrStdout()
	listResp, err := f.Agents().ListAgentTriggers(ctx, connect.NewRequest(&agentsv1.ListAgentTriggersRequest{AgentId: agentID}))
	if err != nil {
		return client.MapError(err)
	}
	byName := map[string][]*agentsv1.AgentTrigger{}
	for _, t := range listResp.Msg.GetTriggers() {
		byName[t.GetName()] = append(byName[t.GetName()], t)
	}
	declared := map[string]struct{}{}
	for _, trigger := range p.Config.Triggers {
		declared[trigger.Name] = struct{}{}
	}

	// Reconcile prior ownership by stable ID before matching by name. A
	// dashboard rename of a managed trigger must not escape ownership and keep
	// firing as an apparently platform-only trigger.
	remoteByID := map[string]*agentsv1.AgentTrigger{}
	for _, triggers := range byName {
		for _, trigger := range triggers {
			remoteByID[trigger.GetId()] = trigger
		}
	}
	for id, previousName := range previousManaged {
		actual := remoteByID[id]
		if actual == nil {
			continue
		}
		if _, stillDeclared := declared[previousName]; !stillDeclared {
			if !force {
				return fmt.Errorf("managed trigger %q was removed from agent.yaml; retry with --force to remove it", previousName)
			}
			if err := deleteAgentTrigger(ctx, f, id); err != nil {
				return fmt.Errorf("trigger %s: remove stale managed trigger: %w", previousName, err)
			}
			removeTriggerFromGroups(byName, id)
			fmt.Fprintf(out, "  trigger %-21s -> removed from managed project\n", previousName)
			continue
		}
		if actual.GetName() != previousName {
			if !force {
				return fmt.Errorf("managed trigger %q was renamed to %q during deployment; retry with --force", previousName, actual.GetName())
			}
			removeTriggerFromGroups(byName, id)
			byName[previousName] = append(byName[previousName], actual)
		}
	}

	for _, t := range p.Config.Triggers {
		name := t.Name

		candidates := byName[name]
		var existing *agentsv1.AgentTrigger
		var platformOnly []*agentsv1.AgentTrigger
		for _, candidate := range candidates {
			if previousManaged[candidate.GetId()] == name {
				existing = candidate
				continue
			}
			platformOnly = append(platformOnly, candidate)
		}
		if len(platformOnly) > 0 {
			return fmt.Errorf(
				"trigger %q conflicts with a platform-only trigger; rename it in agent.yaml or remove the dashboard trigger",
				name,
			)
		}

		if existing != nil && !triggerMatches(existing, t) {
			if !force {
				return fmt.Errorf("trigger %q changed during deployment; retry with --force", name)
			}
			if triggerNeedsRecreate(existing, t) {
				if err := deleteAgentTrigger(ctx, f, existing.GetId()); err != nil {
					return fmt.Errorf("trigger %s: replace old trigger: %w", name, err)
				}
				fmt.Fprintf(out, "  trigger %-21s -> recreating to clear or change fields\n", name)
				existing = nil
			} else {
				if _, err := f.Agents().UpdateAgentTrigger(ctx, connect.NewRequest(buildUpdateTriggerRequest(existing.GetId(), t))); err != nil {
					return fmt.Errorf("trigger %s: %w", name, client.MapError(err))
				}
				fmt.Fprintf(out, "  trigger %-21s -> updated (%s)\n", name, t.Type)
				continue
			}
		}
		if existing != nil {
			fmt.Fprintf(out, "  trigger %-21s -> already converged (%s)\n", name, t.Type)
			continue
		}

		resp, err := f.Agents().CreateAgentTrigger(ctx, connect.NewRequest(buildCreateTriggerRequest(agentID, t)))
		if err != nil {
			return fmt.Errorf("trigger %s: %w", name, client.MapError(err))
		}
		created := resp.Msg.GetTrigger()
		if !t.IsEnabled() {
			if created.GetId() == "" {
				return fmt.Errorf("trigger %s: create returned an empty trigger ID", name)
			}
			if _, err := f.Agents().UpdateAgentTrigger(ctx, connect.NewRequest(buildUpdateTriggerRequest(created.GetId(), t))); err != nil {
				return fmt.Errorf("trigger %s: disable after create: %w", name, client.MapError(err))
			}
		}
		fmt.Fprintf(out, "  trigger %-21s -> created (%s)\n", name, t.Type)
		if url := resp.Msg.GetWebhookUrl(); url != "" {
			fmt.Fprintf(out, "    webhook URL:    %s\n", url)
		}
		if secret := resp.Msg.GetWebhookSecret(); secret != "" {
			fmt.Fprintf(out, "    webhook secret: %s (shown once)\n", secret)
		}
	}

	for name := range byName {
		if _, ok := declared[name]; ok {
			continue
		}
		fmt.Fprintf(out, "  trigger %-21s -> platform-only trigger left untouched\n", name)
	}
	return nil
}

func buildCreateTriggerRequest(agentID string, trigger agentproject.Trigger) *agentsv1.CreateAgentTriggerRequest {
	return &agentsv1.CreateAgentTriggerRequest{
		AgentId:            agentID,
		Name:               trigger.Name,
		TriggerType:        trigger.Type,
		CronExpression:     trigger.Schedule,
		CronTimezone:       trigger.Timezone,
		EventType:          trigger.Event,
		EventSourceAgentId: trigger.SourceAgentID,
		InputTemplate:      trigger.Input,
	}
}

func buildUpdateTriggerRequest(id string, trigger agentproject.Trigger) *agentsv1.UpdateAgentTriggerRequest {
	return &agentsv1.UpdateAgentTriggerRequest{
		Id:                 id,
		Name:               trigger.Name,
		Enabled:            trigger.IsEnabled(),
		CronExpression:     trigger.Schedule,
		CronTimezone:       trigger.Timezone,
		EventType:          trigger.Event,
		EventSourceAgentId: trigger.SourceAgentID,
		InputTemplate:      trigger.Input,
	}
}

func triggerNeedsRecreate(existing *agentsv1.AgentTrigger, desired agentproject.Trigger) bool {
	return existing.GetTriggerType() != desired.Type ||
		(desired.Schedule == "" && existing.GetCronExpression() != "") ||
		(desired.Timezone == "" && existing.GetCronTimezone() != "") ||
		(desired.SourceAgentID == "" && existing.GetEventSourceAgentId() != "") ||
		(desired.Event == "" && existing.GetEventType() != "") ||
		(desired.Input == "" && existing.GetInputTemplate() != "")
}

func deleteAgentTrigger(ctx context.Context, f *client.Factory, id string) error {
	if _, err := f.Agents().DeleteAgentTrigger(ctx, connect.NewRequest(&agentsv1.DeleteAgentTriggerRequest{Id: id})); err != nil {
		return client.MapError(err)
	}
	return nil
}

func removeTriggerFromGroups(groups map[string][]*agentsv1.AgentTrigger, id string) {
	for name, triggers := range groups {
		kept := triggers[:0]
		for _, trigger := range triggers {
			if trigger.GetId() != id {
				kept = append(kept, trigger)
			}
		}
		if len(kept) == 0 {
			delete(groups, name)
		} else {
			groups[name] = kept
		}
	}
}

func applyLimitsCreate(req *agentsv1.CreateAgentRequest, l agentproject.Limits) {
	if l.MaxTurns > 0 {
		v := int32(l.MaxTurns)
		req.MaxTurns = &v
	}
	if l.MaxToolCallsPerTurn > 0 {
		v := int32(l.MaxToolCallsPerTurn)
		req.MaxToolCallsPerTurn = &v
	}
	if l.MaxSteps > 0 {
		v := int32(l.MaxSteps)
		req.MaxSteps = &v
	}
}
