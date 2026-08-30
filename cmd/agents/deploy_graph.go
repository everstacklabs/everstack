package agents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	agentprojectruntime "github.com/everstacklabs/everstack/internal/agents/projectruntime"
	agentrevision "github.com/everstacklabs/everstack/internal/agents/revision"
	"github.com/everstacklabs/everstack/internal/cli/agentproject"
	"github.com/everstacklabs/everstack/internal/cli/client"
	agentsv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	deploymentStampFormat  = 2
	projectionWaitTimeout  = 15 * time.Second
	projectionPollInterval = 100 * time.Millisecond
)

type projectDeployPlan struct {
	project                 *agentproject.Project
	mode                    agentsv1.AgentMode
	desiredUnstampedConfig  *structpb.Struct
	existing                *agentsv1.AgentDefinition
	revision                *agentsv1.AgentRevision
	triggers                map[string][]*agentsv1.AgentTrigger
	previousManagedTriggers map[string]string // trigger ID -> last managed name
	managedTriggers         map[string]string // trigger ID -> current managed name
	links                   []*agentsv1.AgentLink
	needsMutation           bool
	agent                   *agentsv1.AgentDefinition
}

type graphDeployPlan struct {
	root     *projectDeployPlan
	projects []*projectDeployPlan
}

// deployGraph performs every remote conflict check before its first write.
// Any mutating deployment removes the old stamp first, converges definitions,
// immutable source revisions, triggers, and subagent links, then stamps
// subagents followed by the root. A failed partial deployment therefore cannot
// claim success.
func deployGraph(ctx context.Context, f *client.Factory, out io.Writer, root *agentproject.Project, force bool) error {
	projects := append([]*agentproject.Project{root}, root.Subagents...)
	compiledConfigs := make(map[*agentproject.Project]*structpb.Struct, len(projects))
	for _, project := range projects {
		if err := project.EnsureRevisionManifest(); err != nil {
			return fmt.Errorf("compile agent %s revision: %w", project.Config.Name, err)
		}
		config, err := desiredAgentConfig(project, false)
		if err != nil {
			return fmt.Errorf("compile agent %s config: %w", project.Config.Name, err)
		}
		// Validate the stamped form too. It includes graph-relative paths and
		// must be known-good before the first remote mutation.
		if _, err := desiredAgentConfig(project, true); err != nil {
			return fmt.Errorf("compile agent %s deployment stamp: %w", project.Config.Name, err)
		}
		compiledConfigs[project] = config
	}
	plan, err := preflightDeployGraph(ctx, f, root, force)
	if err != nil {
		return err
	}
	for _, project := range plan.projects {
		project.desiredUnstampedConfig = compiledConfigs[project.project]
	}

	mutating := false
	for _, project := range plan.projects {
		mutating = mutating || project.needsMutation
	}
	if !mutating {
		fmt.Fprintf(out, "  agent %-24s -> already converged\n", root.Config.Name)
		return nil
	}
	// The root stamp is the success marker for the complete graph. Any child
	// mutation must invalidate and later refresh it, even when the root's own
	// definition and links were otherwise unchanged.
	plan.root.needsMutation = true

	// Invalidate existing success stamps before changing any dependent
	// resource. A retry after failure must be explicit with --force.
	for _, project := range plan.projects {
		if project.existing == nil || !project.needsMutation {
			continue
		}
		if _, err := f.Agents().UpdateAgent(ctx, connect.NewRequest(&agentsv1.UpdateAgentRequest{
			Id:     project.existing.GetId(),
			Config: project.desiredUnstampedConfig,
		})); err != nil {
			return fmt.Errorf("invalidate deployment stamp for %s: %w", project.project.Config.Name, client.MapError(err))
		}
	}

	for _, project := range plan.projects {
		if !project.needsMutation {
			project.agent = project.existing
			continue
		}
		label := "agent"
		if project.mode == agentsv1.AgentMode_AGENT_MODE_SUBAGENT {
			label = "subagent"
		}
		if project.existing == nil {
			resp, err := f.Agents().CreateAgent(ctx, connect.NewRequest(buildCreateAgentRequest(project.project, project.mode, project.desiredUnstampedConfig)))
			if err != nil {
				return fmt.Errorf("create %s %s: %w", label, project.project.Config.Name, client.MapError(err))
			}
			id := resp.Msg.GetAgent().GetId()
			if id == "" {
				return fmt.Errorf("create %s %s returned an empty agent ID", label, project.project.Config.Name)
			}
			project.agent, err = waitForAgentState(ctx, f, id, project.project, project.mode, false)
			if err != nil {
				return fmt.Errorf("create %s %s: %w", label, project.project.Config.Name, err)
			}
			fmt.Fprintf(out, "  %s %-*s -> created (%s)\n", label, 29-len(label), project.project.Config.Name, id)
		} else {
			id := project.existing.GetId()
			_, err := f.Agents().UpdateAgent(ctx, connect.NewRequest(buildUpdateAgentRequest(id, project.project, project.mode, project.desiredUnstampedConfig)))
			if err != nil {
				return fmt.Errorf("update %s %s: %w", label, project.project.Config.Name, client.MapError(err))
			}
			project.agent, err = waitForAgentState(ctx, f, id, project.project, project.mode, false)
			if err != nil {
				return fmt.Errorf("update %s %s: %w", label, project.project.Config.Name, err)
			}
			fmt.Fprintf(out, "  %s %-*s -> updated (%s)\n", label, 29-len(label), project.project.Config.Name, id)
		}
	}

	for _, project := range plan.projects {
		if !project.needsMutation {
			continue
		}
		rev, created, err := applyRevision(ctx, f, project.agent.GetId(), project.project.RevisionManifest)
		if err != nil {
			return fmt.Errorf("agent %s revision: %w", project.project.Config.Name, err)
		}
		project.revision = rev
		action := "activated"
		if created {
			action = "created"
		}
		fmt.Fprintf(out, "  revision %-21s -> %s (v%d, %s)\n", project.project.Config.Name, action, rev.GetNumber(), shortDigest(rev.GetDigest()))
	}

	command := &cobra.Command{}
	command.SetOut(out)
	command.SetErr(out)
	for _, project := range plan.projects {
		if !project.needsMutation {
			continue
		}
		if err := syncTriggers(ctx, f, command, project.agent.GetId(), project.project, project.previousManagedTriggers, force); err != nil {
			return fmt.Errorf("agent %s: %w", project.project.Config.Name, err)
		}
	}

	if plan.root.needsMutation {
		desiredChildren := make([]*agentsv1.AgentDefinition, 0, len(root.Subagents))
		for _, sub := range plan.projects[1:] {
			desiredChildren = append(desiredChildren, sub.agent)
		}
		if err := syncSubagentLinks(ctx, f, out, plan.root.agent.GetId(), desiredChildren, force); err != nil {
			return err
		}
	}
	if err := verifyGraphResources(ctx, f, plan); err != nil {
		return err
	}

	// Stamp children first. The root hash includes child hashes, so the root
	// must be the final success marker for the complete graph.
	for i := len(plan.projects) - 1; i >= 0; i-- {
		project := plan.projects[i]
		if !project.needsMutation {
			continue
		}
		config, err := desiredAgentConfig(project.project, true, project.managedTriggers)
		if err != nil {
			return err
		}
		_, err = f.Agents().UpdateAgent(ctx, connect.NewRequest(buildUpdateAgentRequest(project.agent.GetId(), project.project, project.mode, config)))
		if err != nil {
			return fmt.Errorf("stamp deployment for %s: %w", project.project.Config.Name, client.MapError(err))
		}
		project.agent, err = waitForAgentState(ctx, f, project.agent.GetId(), project.project, project.mode, true)
		if err != nil {
			return fmt.Errorf("stamp deployment for %s: %w", project.project.Config.Name, err)
		}
	}
	return nil
}

func preflightDeployGraph(ctx context.Context, f *client.Factory, root *agentproject.Project, force bool) (*graphDeployPlan, error) {
	plan := &graphDeployPlan{}
	projects := []*agentproject.Project{root}
	projects = append(projects, root.Subagents...)

	for i, project := range projects {
		mode := agentsv1.AgentMode_AGENT_MODE_SUBAGENT
		if i == 0 {
			mode = agentsv1.AgentMode_AGENT_MODE_PRIMARY
		}
		entry := &projectDeployPlan{
			project:                 project,
			mode:                    mode,
			triggers:                map[string][]*agentsv1.AgentTrigger{},
			previousManagedTriggers: map[string]string{},
			managedTriggers:         map[string]string{},
		}
		var err error
		entry.existing, err = findAgentByName(ctx, f, project.Config.Name)
		if err != nil {
			return nil, err
		}

		triggersMatch := entry.existing == nil
		if entry.existing != nil {
			entry.revision, err = lookupActiveRevision(ctx, f, entry.existing.GetId())
			if err != nil {
				return nil, fmt.Errorf("agent %s revision: %w", project.Config.Name, err)
			}
			revisionMatches := entry.revision != nil && project.RevisionManifest != nil &&
				entry.revision.GetDigest() == project.RevisionManifest.Digest
			entry.previousManagedTriggers = deploymentStampManagedTriggers(entry.existing)
			resp, err := f.Agents().ListAgentTriggers(ctx, connect.NewRequest(&agentsv1.ListAgentTriggersRequest{AgentId: entry.existing.GetId()}))
			if err != nil {
				return nil, client.MapError(err)
			}
			for _, trigger := range resp.Msg.GetTriggers() {
				entry.triggers[trigger.GetName()] = append(entry.triggers[trigger.GetName()], trigger)
			}
			if force && entry.existing.GetConfig().GetFields()[projectMetaKey] == nil {
				entry.previousManagedTriggers = adoptLegacyTriggers(project, entry.triggers)
			}
			triggersMatch = declaredTriggersMatch(project, entry.triggers, entry.previousManagedTriggers)

			definitionMatches := agentMatchesProject(entry.existing, project, mode, true)
			if !force {
				if err := checkDrift(entry.existing, project.Config.Name); err != nil {
					return nil, err
				}
				if deploymentStampHash(entry.existing) != project.Hash() || !definitionMatches || !revisionMatches || !triggersMatch {
					return nil, fmt.Errorf("agent %q differs from this project or one of its managed resources; run `evs agents pull %s` to inspect it, or pass --force to converge", project.Config.Name, project.Config.Name)
				}
			}
			entry.needsMutation = !definitionMatches || !revisionMatches || !triggersMatch
		} else {
			entry.needsMutation = true
		}

		plan.projects = append(plan.projects, entry)
		if i == 0 {
			plan.root = entry
		}
	}

	if plan.root.existing != nil {
		resp, err := f.Agents().ListAgentLinks(ctx, connect.NewRequest(&agentsv1.ListAgentLinksRequest{AgentId: plan.root.existing.GetId()}))
		if err != nil {
			return nil, client.MapError(err)
		}
		plan.root.links = resp.Msg.GetLinks()
		linksMatch := subordinateLinksMatch(plan.root.links, plan.projects[1:])
		if !linksMatch && !force {
			return nil, fmt.Errorf("agent %q subordinate links differ from this project; pass --force to converge them", root.Config.Name)
		}
		plan.root.needsMutation = plan.root.needsMutation || !linksMatch
	}
	return plan, nil
}

func adoptLegacyTriggers(project *agentproject.Project, remote map[string][]*agentsv1.AgentTrigger) map[string]string {
	adopted := map[string]string{}
	for _, desired := range project.Config.Triggers {
		candidates := remote[desired.Name]
		if len(candidates) != 1 {
			continue
		}
		candidate := candidates[0]
		if candidate.GetId() == "" || !triggerMatches(candidate, desired) {
			continue
		}
		adopted[candidate.GetId()] = desired.Name
	}
	return adopted
}

func verifyGraphResources(ctx context.Context, f *client.Factory, plan *graphDeployPlan) error {
	for _, project := range plan.projects {
		active, err := lookupActiveRevision(ctx, f, project.agent.GetId())
		if err != nil {
			return fmt.Errorf("verify agent %s revision: %w", project.project.Config.Name, err)
		}
		if active == nil || project.project.RevisionManifest == nil || active.GetDigest() != project.project.RevisionManifest.Digest {
			return fmt.Errorf("agent %q active revision changed before deployment completed; deployment remains unstamped", project.project.Config.Name)
		}
		triggerResponse, err := f.Agents().ListAgentTriggers(ctx, connect.NewRequest(&agentsv1.ListAgentTriggersRequest{AgentId: project.agent.GetId()}))
		if err != nil {
			return client.MapError(err)
		}
		remoteTriggers := make(map[string][]*agentsv1.AgentTrigger, len(triggerResponse.Msg.GetTriggers()))
		for _, trigger := range triggerResponse.Msg.GetTriggers() {
			remoteTriggers[trigger.GetName()] = append(remoteTriggers[trigger.GetName()], trigger)
		}
		if !declaredTriggersMatch(project.project, remoteTriggers, project.previousManagedTriggers) {
			return fmt.Errorf("agent %q triggers changed before deployment completed; deployment remains unstamped", project.project.Config.Name)
		}
		project.managedTriggers = map[string]string{}
		for _, desired := range project.project.Config.Triggers {
			actual := remoteTriggers[desired.Name]
			if len(actual) != 1 || actual[0].GetId() == "" {
				return fmt.Errorf("agent %q trigger %q has no stable ID; deployment remains unstamped", project.project.Config.Name, desired.Name)
			}
			project.managedTriggers[actual[0].GetId()] = desired.Name
		}
	}

	linkResponse, err := f.Agents().ListAgentLinks(ctx, connect.NewRequest(&agentsv1.ListAgentLinksRequest{AgentId: plan.root.agent.GetId()}))
	if err != nil {
		return client.MapError(err)
	}
	children := make([]*projectDeployPlan, 0, len(plan.projects)-1)
	for _, project := range plan.projects[1:] {
		children = append(children, &projectDeployPlan{existing: project.agent})
	}
	if !subordinateLinksMatch(linkResponse.Msg.GetLinks(), children) {
		return fmt.Errorf("agent %q subordinate links changed before deployment completed; deployment remains unstamped", plan.root.project.Config.Name)
	}
	return nil
}

func desiredAgentConfig(project *agentproject.Project, stamped bool, managed ...map[string]string) (*structpb.Struct, error) {
	config := make(map[string]any, len(project.Config.Config)+2)
	for key, value := range project.Config.Config {
		config[key] = value
	}
	delete(config, projectMetaKey)
	if project.RevisionManifest != nil && len(project.RevisionManifest.Functions) > 0 {
		sandboxConfig := map[string]any{}
		if raw, ok := config["sandbox"]; ok {
			configured, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("agent config sandbox must be an object when project functions are declared")
			}
			for key, value := range configured {
				sandboxConfig[key] = value
			}
			if enabled, explicit := configured["enabled"].(bool); explicit && !enabled {
				return nil, fmt.Errorf("agent config sandbox.enabled cannot be false when project functions are declared")
			}
		}
		sandboxConfig["enabled"] = true
		if _, configured := sandboxConfig["network_mode"]; !configured {
			sandboxConfig["network_mode"] = "deny"
		}
		config["sandbox"] = sandboxConfig
		if err := agentprojectruntime.ValidateFunctionSandboxPolicy(config); err != nil {
			return nil, fmt.Errorf("agent config: %w", err)
		}
	}
	if len(project.Skills) > 0 {
		skills := make([]any, 0, len(project.Skills))
		for _, skill := range project.Skills {
			skills = append(skills, map[string]any{
				"name":        skill.Name,
				"description": skill.Description,
				"content":     skill.Content,
				"source":      "local",
			})
		}
		config["skills"] = skills
	}
	if stamped {
		var triggerIDs map[string]string
		if len(managed) > 0 {
			triggerIDs = managed[0]
		}
		config[projectMetaKey] = map[string]any{
			"source":           "evs deploy",
			"hash":             project.Hash(),
			"format":           deploymentStampFormat,
			"deployed_at":      time.Now().UTC().Format(time.RFC3339),
			"managed_triggers": managedTriggerManifest(project, triggerIDs),
		}
		if len(project.Subagents) > 0 {
			subagentPaths := make(map[string]any, len(project.Subagents))
			for _, subagent := range project.Subagents {
				relative, err := filepath.Rel(project.Dir, subagent.Dir)
				if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
					return nil, fmt.Errorf("subagent %q is outside the root project", subagent.Config.Name)
				}
				subagentPaths[subagent.Config.Name] = "./" + filepath.ToSlash(relative)
			}
			config[projectMetaKey].(map[string]any)["subagent_paths"] = subagentPaths
		}
	}
	result, err := structpb.NewStruct(config)
	if err != nil {
		return nil, fmt.Errorf("agent config is not JSON-compatible: %w", err)
	}
	return result, nil
}

func desiredToolNames(project *agentproject.Project) []string {
	tools := append([]string(nil), project.BuiltinTools...)
	for _, tool := range project.ToolFiles {
		tools = append(tools, tool.Name)
	}
	return tools
}

func buildCreateAgentRequest(project *agentproject.Project, mode agentsv1.AgentMode, config *structpb.Struct) *agentsv1.CreateAgentRequest {
	description := project.Config.Description
	request := &agentsv1.CreateAgentRequest{
		Name:               project.Config.Name,
		Description:        &description,
		Model:              project.Config.Model,
		SystemPrompt:       &project.Instructions,
		Tools:              desiredToolNames(project),
		Config:             config,
		Mode:               mode,
		TaskPermissionMode: taskModeToProto(project.Config.Permissions.TaskMode),
	}
	applyLimitsCreate(request, project.Config.Limits)
	return request
}

func buildUpdateAgentRequest(id string, project *agentproject.Project, mode agentsv1.AgentMode, config *structpb.Struct) *agentsv1.UpdateAgentRequest {
	description := project.Config.Description
	model := project.Config.Model
	prompt := project.Instructions
	maxTurns := int32(project.Config.Limits.MaxTurns)
	maxToolCalls := int32(project.Config.Limits.MaxToolCallsPerTurn)
	maxSteps := int32(project.Config.Limits.MaxSteps)
	taskMode := taskModeToProto(project.Config.Permissions.TaskMode)
	request := &agentsv1.UpdateAgentRequest{
		Id:                  id,
		Description:         &description,
		Model:               &model,
		SystemPrompt:        &prompt,
		Config:              config,
		MaxTurns:            &maxTurns,
		MaxToolCallsPerTurn: &maxToolCalls,
		Mode:                &mode,
		MaxSteps:            &maxSteps,
		TaskPermissionMode:  &taskMode,
	}
	tools := desiredToolNames(project)
	if len(tools) == 0 {
		clear := true
		request.ClearTools = &clear
	} else {
		request.Tools = tools
	}
	return request
}

func agentMatchesProject(agent *agentsv1.AgentDefinition, project *agentproject.Project, mode agentsv1.AgentMode, stamped bool) bool {
	if agent == nil {
		return false
	}
	config, err := desiredAgentConfig(project, stamped)
	if err != nil {
		return false
	}
	return agent.GetName() == project.Config.Name &&
		agent.GetDescription() == project.Config.Description &&
		agent.GetModel() == project.Config.Model &&
		agent.GetSystemPrompt() == project.Instructions &&
		reflect.DeepEqual(agent.GetTools(), desiredToolNames(project)) &&
		reflect.DeepEqual(comparableAgentConfig(agent.GetConfig().AsMap()), comparableAgentConfig(config.AsMap())) &&
		agent.GetMaxTurns() == int32(project.Config.Limits.MaxTurns) &&
		agent.GetMaxToolCallsPerTurn() == int32(project.Config.Limits.MaxToolCallsPerTurn) &&
		agent.GetMaxSteps() == int32(project.Config.Limits.MaxSteps) &&
		agent.GetMode() == mode &&
		agent.GetTaskPermissionMode() == taskModeToProto(project.Config.Permissions.TaskMode)
}

func deploymentStampHash(agent *agentsv1.AgentDefinition) string {
	meta := agent.GetConfig().GetFields()[projectMetaKey].GetStructValue()
	return meta.GetFields()["hash"].GetStringValue()
}

func comparableAgentConfig(config map[string]any) map[string]any {
	result := make(map[string]any, len(config))
	for key, value := range config {
		result[key] = value
	}
	meta, ok := result[projectMetaKey].(map[string]any)
	if !ok {
		return result
	}
	metaCopy := make(map[string]any, len(meta))
	for key, value := range meta {
		switch key {
		case "deployed_at":
			continue
		case "managed_triggers":
			names := []string{}
			if entries, ok := value.([]any); ok {
				for _, entry := range entries {
					if record, ok := entry.(map[string]any); ok {
						if name, ok := record["name"].(string); ok {
							names = append(names, name)
						}
					}
				}
			}
			sort.Strings(names)
			normalized := make([]any, len(names))
			for i, name := range names {
				normalized[i] = name
			}
			metaCopy[key] = normalized
		default:
			metaCopy[key] = value
		}
	}
	result[projectMetaKey] = metaCopy
	return result
}

func lookupActiveRevision(ctx context.Context, f *client.Factory, agentID string) (*agentsv1.AgentRevision, error) {
	resp, err := f.Agents().GetActiveAgentRevision(ctx, connect.NewRequest(&agentsv1.GetActiveAgentRevisionRequest{AgentId: agentID}))
	if err != nil {
		if connectErrorHasCode(err, connect.CodeNotFound) {
			return nil, nil
		}
		if connectErrorHasCode(err, connect.CodeUnimplemented) {
			return nil, fmt.Errorf("server does not support immutable agent revisions; upgrade the Everstack gateway before deploying this project")
		}
		return nil, client.MapError(err)
	}
	if resp.Msg.GetRevision() == nil {
		return nil, fmt.Errorf("active revision lookup returned an empty revision")
	}
	return resp.Msg.GetRevision(), nil
}

func applyRevision(
	ctx context.Context,
	f *client.Factory,
	agentID string,
	manifest *agentrevision.Manifest,
) (*agentsv1.AgentRevision, bool, error) {
	if manifest == nil {
		return nil, false, fmt.Errorf("compiled revision manifest is missing")
	}
	request := &agentsv1.CreateAgentRevisionRequest{AgentId: agentID}
	for _, file := range manifest.Files {
		request.Files = append(request.Files, &agentsv1.AgentRevisionFile{
			Path: file.Path, Content: append([]byte(nil), file.Content...), Mode: file.Mode,
		})
	}
	for _, function := range manifest.Functions {
		var parameters *structpb.Struct
		if len(function.Parameters) > 0 {
			var err error
			parameters, err = structpb.NewStruct(function.Parameters)
			if err != nil {
				return nil, false, fmt.Errorf("function %s parameters: %w", function.Name, err)
			}
		}
		request.Functions = append(request.Functions, &agentsv1.AgentProjectFunction{
			Name: function.Name, Description: function.Description, Path: function.Path,
			ExportName: function.Export, Runtime: string(function.Runtime), Parameters: parameters,
		})
	}
	resp, err := f.Agents().CreateAgentRevision(ctx, connect.NewRequest(request))
	if err != nil {
		return nil, false, client.MapError(err)
	}
	if resp.Msg.GetRevision() == nil || resp.Msg.GetRevision().GetId() == "" {
		return nil, false, fmt.Errorf("create revision returned an empty revision")
	}
	if resp.Msg.GetRevision().GetDigest() != manifest.Digest {
		return nil, false, fmt.Errorf("server returned revision digest %s, want %s", resp.Msg.GetRevision().GetDigest(), manifest.Digest)
	}
	return resp.Msg.GetRevision(), resp.Msg.GetCreated(), nil
}

func shortDigest(digest string) string {
	if len(digest) <= 12 {
		return digest
	}
	return digest[:12]
}

func managedTriggerManifest(project *agentproject.Project, ids map[string]string) []any {
	entries := make([]map[string]any, 0, len(project.Config.Triggers))
	for _, trigger := range project.Config.Triggers {
		id := ""
		for candidateID, name := range ids {
			if name == trigger.Name {
				id = candidateID
				break
			}
		}
		entries = append(entries, map[string]any{"id": id, "name": trigger.Name})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i]["name"].(string) < entries[j]["name"].(string)
	})
	result := make([]any, len(entries))
	for i, entry := range entries {
		result[i] = entry
	}
	return result
}

func deploymentStampManagedTriggers(agent *agentsv1.AgentDefinition) map[string]string {
	managed := map[string]string{}
	meta := agent.GetConfig().GetFields()[projectMetaKey].GetStructValue()
	for _, value := range meta.GetFields()["managed_triggers"].GetListValue().GetValues() {
		record := value.GetStructValue()
		id := record.GetFields()["id"].GetStringValue()
		name := record.GetFields()["name"].GetStringValue()
		if id != "" && name != "" {
			managed[id] = name
		}
	}
	return managed
}

func waitForAgentState(
	ctx context.Context,
	f *client.Factory,
	id string,
	project *agentproject.Project,
	mode agentsv1.AgentMode,
	stamped bool,
) (*agentsv1.AgentDefinition, error) {
	waitCtx, cancel := context.WithTimeout(ctx, projectionWaitTimeout)
	defer cancel()
	for {
		resp, err := f.Agents().GetAgent(waitCtx, connect.NewRequest(&agentsv1.GetAgentRequest{Id: id}))
		if err == nil && agentMatchesProject(resp.Msg.GetAgent(), project, mode, stamped) {
			return resp.Msg.GetAgent(), nil
		}
		if err != nil && !connectErrorHasCode(err, connect.CodeNotFound) {
			return nil, client.MapError(err)
		}
		select {
		case <-waitCtx.Done():
			return nil, fmt.Errorf("agent %q did not reach the requested projected state before timeout; deployment remains unstamped", project.Config.Name)
		case <-time.After(projectionPollInterval):
		}
	}
}

func connectErrorHasCode(err error, code connect.Code) bool {
	var connectErr *connect.Error
	return errors.As(err, &connectErr) && connectErr.Code() == code
}

func declaredTriggersMatch(
	project *agentproject.Project,
	remote map[string][]*agentsv1.AgentTrigger,
	previousManaged map[string]string,
) bool {
	declared := make(map[string]struct{}, len(project.Config.Triggers))
	remoteByID := map[string]*agentsv1.AgentTrigger{}
	for _, triggers := range remote {
		for _, trigger := range triggers {
			remoteByID[trigger.GetId()] = trigger
		}
	}
	for _, desired := range project.Config.Triggers {
		declared[desired.Name] = struct{}{}
		managedID := ""
		for id, name := range previousManaged {
			if name == desired.Name {
				managedID = id
				break
			}
		}
		actual := remoteByID[managedID]
		byName := remote[desired.Name]
		if managedID == "" || actual == nil || len(byName) != 1 || byName[0].GetId() != managedID || !triggerMatches(actual, desired) {
			return false
		}
	}
	for _, name := range previousManaged {
		if _, stillDeclared := declared[name]; !stillDeclared {
			return false
		}
	}
	return true
}

func triggerMatches(actual *agentsv1.AgentTrigger, desired agentproject.Trigger) bool {
	return actual != nil &&
		actual.GetName() == desired.Name &&
		actual.GetEnabled() == desired.IsEnabled() &&
		actual.GetTriggerType() == desired.Type &&
		actual.GetCronExpression() == desired.Schedule &&
		actual.GetCronTimezone() == desired.Timezone &&
		actual.GetEventSourceAgentId() == desired.SourceAgentID &&
		actual.GetEventType() == desired.Event &&
		actual.GetInputTemplate() == desired.Input
}

func subordinateLinksMatch(links []*agentsv1.AgentLink, children []*projectDeployPlan) bool {
	desired := make(map[string]struct{}, len(children))
	for _, child := range children {
		if child.existing == nil {
			return false
		}
		desired[child.existing.GetId()] = struct{}{}
	}
	seen := map[string]struct{}{}
	for _, link := range links {
		if link.GetLinkType() != agentsv1.AgentLinkType_AGENT_LINK_TYPE_SUBORDINATE || link.GetTargetType() != agentLinkTargetAgent {
			continue
		}
		if _, ok := desired[link.GetTargetId()]; !ok {
			return false
		}
		if _, duplicate := seen[link.GetTargetId()]; duplicate {
			return false
		}
		seen[link.GetTargetId()] = struct{}{}
	}
	return len(seen) == len(desired)
}

func syncSubagentLinks(ctx context.Context, f *client.Factory, out io.Writer, parentID string, children []*agentsv1.AgentDefinition, force bool) error {
	resp, err := f.Agents().ListAgentLinks(ctx, connect.NewRequest(&agentsv1.ListAgentLinksRequest{AgentId: parentID}))
	if err != nil {
		return client.MapError(err)
	}
	desired := make(map[string]*agentsv1.AgentDefinition, len(children))
	for _, child := range children {
		desired[child.GetId()] = child
	}
	seen := map[string]struct{}{}
	for _, link := range resp.Msg.GetLinks() {
		if link.GetLinkType() != agentsv1.AgentLinkType_AGENT_LINK_TYPE_SUBORDINATE || link.GetTargetType() != agentLinkTargetAgent {
			continue
		}
		_, wanted := desired[link.GetTargetId()]
		_, duplicate := seen[link.GetTargetId()]
		if wanted && !duplicate {
			seen[link.GetTargetId()] = struct{}{}
			continue
		}
		if !force {
			return fmt.Errorf("subordinate links changed during deployment; retry with --force")
		}
		if _, err := f.Agents().DeleteAgentLink(ctx, connect.NewRequest(&agentsv1.DeleteAgentLinkRequest{LinkId: link.GetId()})); err != nil {
			return client.MapError(err)
		}
		fmt.Fprintf(out, "  link %-24s -> removed stale subordinate\n", link.GetTargetName())
	}
	for id, child := range desired {
		if _, ok := seen[id]; ok {
			continue
		}
		name := child.GetName()
		if _, err := f.Agents().CreateAgentLink(ctx, connect.NewRequest(&agentsv1.CreateAgentLinkRequest{
			SourceAgentId: parentID,
			TargetType:    agentLinkTargetAgent,
			TargetId:      id,
			TargetName:    &name,
			LinkType:      agentsv1.AgentLinkType_AGENT_LINK_TYPE_SUBORDINATE,
			Protocol:      agentsv1.AgentLinkProtocol_AGENT_LINK_PROTOCOL_INTERNAL,
		})); err != nil {
			return client.MapError(err)
		}
		fmt.Fprintf(out, "  link %-24s -> subordinate of parent\n", name)
	}
	return nil
}
