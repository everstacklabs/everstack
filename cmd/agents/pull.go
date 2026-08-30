package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"connectrpc.com/connect"
	agentrevision "github.com/everstacklabs/everstack/internal/agents/revision"
	agenttrigger "github.com/everstacklabs/everstack/internal/agents/trigger"
	"github.com/everstacklabs/everstack/internal/cli/agentproject"
	"github.com/everstacklabs/everstack/internal/cli/client"
	"github.com/everstacklabs/everstack/internal/functions/isolation"
	agentsv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	functionsv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/functions/v1"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newPullCmd(options *connectionOptions) *cobra.Command {
	var dir string
	var force bool
	cmd := &cobra.Command{
		Use:   "pull <name>",
		Short: "Export a platform agent into the directory project format",
		Long: `Export a deployed agent back into its directory project format.
Revision-backed agents restore their source tree and overlay the current
dashboard-editable definition; older agents are reconstructed as agent.yaml,
instructions.md, skills/, tools/, and subagents/.
Use it to reconcile dashboard edits before the next ` + "`evs deploy`" + `.
If deployed source removed local project files, inspect them first and pass
--force to remove only those stale source files.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			target := dir
			if target == "" {
				if err := validateRemotePathSegment(name, "agent name used as the default output directory"); err != nil {
					return err
				}
				target = name
			}
			return runPull(cmd, name, target, force, *options)
		},
	}
	cmd.Flags().StringVarP(&dir, "dir", "d", "", "target directory (default: the agent name)")
	cmd.Flags().BoolVar(&force, "force", false, "remove stale local project files that are absent from the deployed revision")
	return cmd
}

// pullSummary counts what a pull wrote, for the closing report.
type pullSummary struct {
	skills    int
	tools     int
	subagents int
	warnings  []string
}

func runPull(cmd *cobra.Command, name, dir string, force bool, options connectionOptions) error {
	f, err := requireFactory(options)
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	agent, err := findAgentByName(ctx, f, name)
	if err != nil {
		return err
	}
	if agent == nil {
		return fmt.Errorf("no agent named %q", name)
	}

	sum, err := pullAgentIntoWithForce(ctx, f, dir, agent, true, force)
	if err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(),
		"Pulled agent %s into %s/ (agent.yaml, instructions.md, %d skills, %d tool entries, %d subagents)\n",
		name, dir, sum.skills, sum.tools, sum.subagents)
	for _, warning := range sum.warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s\n", warning)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Review the diff, then `evs deploy` to converge.")
	return nil
}

// pullAgentInto fetches and validates the complete remote graph before it
// writes the first destination file. This prevents a late RPC or validation
// failure from leaving an existing project half replaced.
func pullAgentInto(
	ctx context.Context,
	f *client.Factory,
	dir string,
	agent *agentsv1.AgentDefinition,
	withSubagents bool,
) (pullSummary, error) {
	return pullAgentIntoWithForce(ctx, f, dir, agent, withSubagents, false)
}

func pullAgentIntoWithForce(
	ctx context.Context,
	f *client.Factory,
	dir string,
	agent *agentsv1.AgentDefinition,
	withSubagents bool,
	force bool,
) (pullSummary, error) {
	if withSubagents && agent.GetMode() == agentsv1.AgentMode_AGENT_MODE_SUBAGENT {
		return pullSummary{}, fmt.Errorf("agent %q is a subagent; pull its primary parent to preserve the subordinate relationship", agent.GetName())
	}
	bundle := &pullBundle{files: map[string]pullFile{}, materializedFunctions: map[string]struct{}{}}
	if err := collectPulledAgent(ctx, f, agent, withSubagents, "", bundle); err != nil {
		return pullSummary{}, err
	}

	stage, err := os.MkdirTemp("", "evs-pull-validate-*")
	if err != nil {
		return pullSummary{}, err
	}
	defer os.RemoveAll(stage)
	if err := writePullBundle(stage, bundle); err != nil {
		return pullSummary{}, err
	}
	if _, err := agentproject.Load(stage); err != nil {
		return pullSummary{}, fmt.Errorf("remote agent cannot be represented as a deployable project: %w", err)
	}

	root, err := canonicalOutputRoot(dir)
	if err != nil {
		return pullSummary{}, err
	}
	if err := reconcileStaleProjectFiles(root, bundle, force); err != nil {
		return pullSummary{}, err
	}
	if err := writePullBundle(root, bundle); err != nil {
		return pullSummary{}, err
	}
	return bundle.summary, nil
}

func reconcileStaleProjectFiles(root string, bundle *pullBundle, force bool) error {
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	root = canonicalRoot
	agentYAML := filepath.Join(root, "agent.yaml")
	if _, err := os.Lstat(agentYAML); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	project, err := agentproject.Load(root)
	if err != nil {
		return fmt.Errorf("existing pull target is not a valid agent project: %w", err)
	}
	localFiles := make(map[string]struct{})
	var collect func(*agentproject.Project) error
	collect = func(current *agentproject.Project) error {
		prefix, err := filepath.Rel(root, current.Dir)
		if err != nil || prefix == ".." || strings.HasPrefix(prefix, ".."+string(filepath.Separator)) {
			return fmt.Errorf("existing subagent is outside the pull target")
		}
		if prefix == "." {
			prefix = ""
		}
		for _, file := range current.RevisionManifest.Files {
			localPath := filepath.Join(prefix, filepath.FromSlash(file.Path))
			localFiles[filepath.ToSlash(filepath.Clean(localPath))] = struct{}{}
		}
		for _, subagent := range current.Subagents {
			if err := collect(subagent); err != nil {
				return err
			}
		}
		return nil
	}
	if err := collect(project); err != nil {
		return err
	}
	stale := make([]string, 0)
	for filePath := range localFiles {
		if _, exists := bundle.files[filePath]; !exists {
			stale = append(stale, filePath)
		}
	}
	sort.Strings(stale)
	if len(stale) == 0 {
		return nil
	}
	if !force {
		return fmt.Errorf("pull would remove %d stale local project file(s), including %q; review them and rerun with --force", len(stale), filepath.ToSlash(stale[0]))
	}
	for _, filePath := range stale {
		target, err := secureProjectPath(root, strings.Split(filepath.ToSlash(filePath), "/")...)
		if err != nil {
			return err
		}
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale project file %s: %w", filepath.ToSlash(filePath), err)
		}
	}
	return nil
}

type pullBundle struct {
	files                 map[string]pullFile
	materializedFunctions map[string]struct{}
	summary               pullSummary
}

type pullFile struct {
	data []byte
	mode os.FileMode
}

func (b *pullBundle) add(path string, data []byte) error {
	return b.addWithMode(path, data, 0o644)
}

func (b *pullBundle) addWithMode(path string, data []byte, mode os.FileMode) error {
	path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if _, exists := b.files[path]; exists {
		return fmt.Errorf("remote graph maps more than one resource to %s", path)
	}
	if mode == 0 {
		mode = 0o644
	}
	if mode.Perm() != mode || mode > 0o777 {
		return fmt.Errorf("remote file %s has invalid mode %#o", path, mode)
	}
	b.files[path] = pullFile{data: append([]byte(nil), data...), mode: mode}
	return nil
}

func collectPulledAgent(
	ctx context.Context,
	f *client.Factory,
	agent *agentsv1.AgentDefinition,
	withSubagents bool,
	prefix string,
	bundle *pullBundle,
) error {
	if f != nil {
		revision, err := fetchActiveRevisionForPull(ctx, f, agent.GetId())
		if err != nil {
			return err
		}
		if revision != nil {
			return collectRevisionPulledAgent(ctx, f, agent, revision, withSubagents, prefix, bundle)
		}
	}
	return collectLegacyPulledAgent(ctx, f, agent, withSubagents, prefix, bundle)
}

func collectLegacyPulledAgent(
	ctx context.Context,
	f *client.Factory,
	agent *agentsv1.AgentDefinition,
	withSubagents bool,
	prefix string,
	bundle *pullBundle,
) error {
	if strings.TrimSpace(agent.GetName()) == "" {
		return fmt.Errorf("remote agent has an empty name")
	}
	if err := bundle.add(filepath.Join(prefix, "instructions.md"), []byte(agent.GetSystemPrompt()+"\n")); err != nil {
		return err
	}

	cfg := agent.GetConfig().AsMap()
	skillPaths := []string{}
	if rawSkills, ok := cfg["skills"].([]any); ok {
		for _, raw := range rawSkills {
			skill, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name, _ := skill["name"].(string)
			description, _ := skill["description"].(string)
			content, _ := skill["content"].(string)
			if name == "" || content == "" {
				continue
			}
			if err := validateRemotePathSegment(name, "skill name"); err != nil {
				return err
			}
			document, err := renderPulledSkill(name, description, content)
			if err != nil {
				return fmt.Errorf("skill %s: %w", name, err)
			}
			if err := bundle.add(filepath.Join(prefix, "skills", name, "SKILL.md"), document); err != nil {
				return err
			}
			skillPaths = append(skillPaths, "./skills/"+name)
			bundle.summary.skills++
		}
	}

	toolEntries := []string{}
	for _, tool := range agent.GetTools() {
		entry, files, pulled, err := fetchPulledTool(ctx, f, tool)
		if err != nil {
			return err
		}
		if !pulled {
			toolEntries = append(toolEntries, tool)
			continue
		}
		if _, exists := bundle.materializedFunctions[tool]; exists {
			// Functions are tenant-global. One project owns the source file and
			// every other graph node references that same Function by name.
			toolEntries = append(toolEntries, tool)
			continue
		}
		bundle.materializedFunctions[tool] = struct{}{}
		toolEntries = append(toolEntries, entry)
		for path, data := range files {
			if err := bundle.add(filepath.Join(prefix, filepath.FromSlash(path)), data); err != nil {
				return err
			}
		}
	}
	bundle.summary.tools += len(toolEntries)

	triggerResponse, err := f.Agents().ListAgentTriggers(ctx, connect.NewRequest(&agentsv1.ListAgentTriggersRequest{AgentId: agent.GetId()}))
	if err != nil {
		return client.MapError(err)
	}
	pullableTriggers, warnings := pullableLegacyTriggers(agent.GetName(), triggerResponse.Msg.GetTriggers())
	bundle.summary.warnings = append(bundle.summary.warnings, warnings...)
	triggers := make([]map[string]any, 0, len(pullableTriggers))
	for _, trigger := range pullableTriggers {
		entry := map[string]any{"type": trigger.GetTriggerType(), "name": trigger.GetName()}
		if !trigger.GetEnabled() {
			entry["enabled"] = false
		}
		if trigger.GetCronExpression() != "" {
			entry["schedule"] = trigger.GetCronExpression()
		}
		if trigger.GetCronTimezone() != "" {
			entry["timezone"] = trigger.GetCronTimezone()
		}
		if trigger.GetEventSourceAgentId() != "" {
			entry["source_agent_id"] = trigger.GetEventSourceAgentId()
		}
		if trigger.GetEventType() != "" {
			entry["event"] = trigger.GetEventType()
		}
		if trigger.GetInputTemplate() != "" {
			entry["input"] = trigger.GetInputTemplate()
		}
		triggers = append(triggers, entry)
	}

	subPaths := []string{}
	if withSubagents {
		linkResponse, err := f.Agents().ListAgentLinks(ctx, connect.NewRequest(&agentsv1.ListAgentLinksRequest{AgentId: agent.GetId()}))
		if err != nil {
			return client.MapError(err)
		}
		for _, link := range linkResponse.Msg.GetLinks() {
			if link.GetLinkType() != agentsv1.AgentLinkType_AGENT_LINK_TYPE_SUBORDINATE || link.GetTargetType() != agentLinkTargetAgent {
				continue
			}
			response, err := f.Agents().GetAgent(ctx, connect.NewRequest(&agentsv1.GetAgentRequest{Id: link.GetTargetId()}))
			if err != nil {
				if connectErrorHasCode(err, connect.CodeNotFound) {
					return fmt.Errorf("subordinate link %q targets missing agent %q", link.GetId(), link.GetTargetId())
				}
				return client.MapError(err)
			}
			subagent := response.Msg.GetAgent()
			if subagent == nil {
				return fmt.Errorf("subordinate link %q returned an empty agent for target %q", link.GetId(), link.GetTargetId())
			}
			if subagent.GetMode() != agentsv1.AgentMode_AGENT_MODE_SUBAGENT {
				return fmt.Errorf("subordinate link %q targets agent %q without subagent mode", link.GetId(), link.GetTargetId())
			}
			if strings.TrimSpace(subagent.GetName()) == "" {
				return fmt.Errorf("subordinate link %q targets an agent with an empty name", link.GetId())
			}
			if err := validateRemotePathSegment(subagent.GetName(), "subagent name"); err != nil {
				return err
			}
			subPrefix := filepath.Join(prefix, "subagents", subagent.GetName())
			if err := collectPulledAgent(ctx, f, subagent, false, subPrefix, bundle); err != nil {
				return fmt.Errorf("subagent %s: %w", subagent.GetName(), err)
			}
			subPaths = append(subPaths, "./subagents/"+subagent.GetName())
			bundle.summary.subagents++
		}
	}

	delete(cfg, "skills")
	delete(cfg, projectMetaKey)
	document := map[string]any{
		"name":         agent.GetName(),
		"description":  agent.GetDescription(),
		"model":        agent.GetModel(),
		"instructions": "./instructions.md",
		"limits": map[string]any{
			"max_turns":               agent.GetMaxTurns(),
			"max_tool_calls_per_turn": agent.GetMaxToolCallsPerTurn(),
		},
		"tools": toolEntries,
	}
	if mode := taskModeFromProto(agent.GetTaskPermissionMode()); mode != "" {
		document["permissions"] = map[string]any{"task_mode": mode}
	}
	if agent.GetMaxSteps() > 0 {
		document["limits"].(map[string]any)["max_steps"] = agent.GetMaxSteps()
	}
	if len(cfg) > 0 {
		document["config"] = cfg
	}
	if len(skillPaths) > 0 {
		document["skills"] = skillPaths
	}
	if len(subPaths) > 0 {
		document["subagents"] = subPaths
	}
	if len(triggers) > 0 {
		document["triggers"] = triggers
	}
	data, err := yaml.Marshal(document)
	if err != nil {
		return err
	}
	return bundle.add(filepath.Join(prefix, "agent.yaml"), data)
}

func pullableLegacyTriggers(agentName string, remote []*agentsv1.AgentTrigger) ([]*agentsv1.AgentTrigger, []string) {
	byName := make(map[string][]*agentsv1.AgentTrigger, len(remote))
	for _, candidate := range remote {
		if candidate == nil {
			continue
		}
		name := strings.TrimSpace(candidate.GetName())
		byName[name] = append(byName[name], candidate)
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([]*agentsv1.AgentTrigger, 0, len(remote))
	warnings := []string{}
	for _, name := range names {
		group := byName[name]
		if name == "" {
			warnings = append(warnings, fmt.Sprintf("agent %q has %d unnamed trigger record(s); they were left dashboard-owned and omitted from agent.yaml", agentName, len(group)))
			continue
		}
		if len(group) != 1 {
			warnings = append(warnings, fmt.Sprintf("agent %q has %d trigger records named %q; they were left dashboard-owned and omitted from agent.yaml", agentName, len(group), name))
			continue
		}
		candidate := group[0]
		if reason := unrepresentableLegacyTrigger(candidate); reason != "" {
			warnings = append(warnings, fmt.Sprintf("agent %q trigger %q %s; it was left dashboard-owned and omitted from agent.yaml", agentName, name, reason))
			continue
		}
		result = append(result, candidate)
	}
	return result, warnings
}

func unrepresentableLegacyTrigger(candidate *agentsv1.AgentTrigger) string {
	if candidate.GetTriggerType() == string(agenttrigger.TriggerWebhook) && strings.TrimSpace(candidate.GetWebhookPath()) == "" {
		return "has no webhook path"
	}
	if err := agenttrigger.ValidateConfiguration(&agenttrigger.Trigger{
		Type:               agenttrigger.TriggerType(candidate.GetTriggerType()),
		CronExpression:     candidate.GetCronExpression(),
		CronTimezone:       candidate.GetCronTimezone(),
		EventSourceAgentID: candidate.GetEventSourceAgentId(),
		EventType:          candidate.GetEventType(),
	}); err != nil {
		return fmt.Sprintf("has invalid configuration: %v", err)
	}
	return ""
}

func fetchActiveRevisionForPull(ctx context.Context, f *client.Factory, agentID string) (*agentsv1.AgentRevision, error) {
	resp, err := f.Agents().GetActiveAgentRevision(ctx, connect.NewRequest(&agentsv1.GetActiveAgentRevisionRequest{AgentId: agentID}))
	if err != nil {
		if connectErrorHasCode(err, connect.CodeNotFound) || connectErrorHasCode(err, connect.CodeUnimplemented) {
			return nil, nil
		}
		return nil, client.MapError(err)
	}
	if resp.Msg.GetRevision() == nil {
		return nil, fmt.Errorf("active revision lookup for agent %q returned an empty revision", agentID)
	}
	return resp.Msg.GetRevision(), nil
}

func collectRevisionPulledAgent(
	ctx context.Context,
	f *client.Factory,
	agent *agentsv1.AgentDefinition,
	remote *agentsv1.AgentRevision,
	withSubagents bool,
	prefix string,
	bundle *pullBundle,
) error {
	manifest, err := validatePulledRevision(agent, remote)
	if err != nil {
		return err
	}
	source, err := pulledRevisionConfig(manifest)
	if err != nil {
		return err
	}
	triggers, err := currentManagedTriggersForPull(ctx, f, agent, source.Triggers)
	if err != nil {
		return err
	}
	subagents := []revisionPulledSubagent{}
	subagentDeclarations := append([]string(nil), source.Subagents...)
	if withSubagents {
		subagents, subagentDeclarations, err = currentRevisionSubagentsForPull(ctx, f, agent, manifest)
		if err != nil {
			return err
		}
	}
	files, err := overlayRevisionDefinition(manifest, source, agent, triggers, subagentDeclarations)
	if err != nil {
		return err
	}
	for _, file := range files {
		if err := bundle.addWithMode(filepath.Join(prefix, filepath.FromSlash(file.Path)), file.Content, os.FileMode(file.Mode)); err != nil {
			return err
		}
		if isPulledSkillFile(file.Path) {
			bundle.summary.skills++
		}
	}
	bundle.summary.tools += len(agent.GetTools())

	for _, subagent := range subagents {
		if err := collectPulledAgent(ctx, f, subagent.agent, false, filepath.Join(prefix, filepath.FromSlash(subagent.path)), bundle); err != nil {
			return fmt.Errorf("subagent %s: %w", subagent.agent.GetName(), err)
		}
		bundle.summary.subagents++
	}
	return nil
}

type revisionPulledSubagent struct {
	agent *agentsv1.AgentDefinition
	path  string
}

func pulledRevisionConfig(manifest *agentrevision.Manifest) (agentproject.Config, error) {
	for _, file := range manifest.Files {
		if file.Path != "agent.yaml" {
			continue
		}
		var config agentproject.Config
		if err := yaml.Unmarshal(file.Content, &config); err != nil {
			return agentproject.Config{}, fmt.Errorf("active revision agent.yaml: %w", err)
		}
		return config, nil
	}
	return agentproject.Config{}, fmt.Errorf("active revision does not contain agent.yaml")
}

func currentManagedTriggersForPull(
	ctx context.Context,
	f *client.Factory,
	agent *agentsv1.AgentDefinition,
	source []agentproject.Trigger,
) ([]agentproject.Trigger, error) {
	managed := deploymentStampManagedTriggers(agent)
	if len(managed) == 0 {
		return append([]agentproject.Trigger(nil), source...), nil
	}
	response, err := f.Agents().ListAgentTriggers(ctx, connect.NewRequest(&agentsv1.ListAgentTriggersRequest{AgentId: agent.GetId()}))
	if err != nil {
		return nil, client.MapError(err)
	}
	remoteByID := make(map[string]*agentsv1.AgentTrigger, len(response.Msg.GetTriggers()))
	for _, trigger := range response.Msg.GetTriggers() {
		remoteByID[trigger.GetId()] = trigger
	}
	managedByName := make(map[string]string, len(managed))
	for id, name := range managed {
		managedByName[name] = id
	}

	result := make([]agentproject.Trigger, 0, len(managed))
	seen := make(map[string]struct{}, len(managed))
	for index, trigger := range source {
		effective := effectivePulledTrigger(trigger, agent.GetName(), index)
		id := managedByName[effective.Name]
		actual := remoteByID[id]
		if actual == nil {
			continue
		}
		result = append(result, pulledTrigger(actual, &trigger, agent.GetName(), index))
		seen[id] = struct{}{}
	}
	extraIDs := make([]string, 0)
	for id := range managed {
		if _, ok := seen[id]; ok || remoteByID[id] == nil {
			continue
		}
		extraIDs = append(extraIDs, id)
	}
	sort.Slice(extraIDs, func(i, j int) bool {
		return remoteByID[extraIDs[i]].GetName() < remoteByID[extraIDs[j]].GetName()
	})
	for _, id := range extraIDs {
		result = append(result, pulledTrigger(remoteByID[id], nil, agent.GetName(), len(result)))
	}
	return result, nil
}

func effectivePulledTrigger(trigger agentproject.Trigger, agentName string, index int) agentproject.Trigger {
	trigger.Name = strings.TrimSpace(trigger.Name)
	if trigger.Name == "" {
		trigger.Name = fmt.Sprintf("%s-%s-%d", agentName, trigger.Type, index+1)
	}
	if strings.TrimSpace(trigger.Timezone) == "" {
		trigger.Timezone = "UTC"
	}
	if trigger.Enabled == nil {
		enabled := true
		trigger.Enabled = &enabled
	}
	return trigger
}

func pulledTrigger(
	current *agentsv1.AgentTrigger,
	source *agentproject.Trigger,
	agentName string,
	index int,
) agentproject.Trigger {
	if source == nil {
		result := agentproject.Trigger{
			Type: current.GetTriggerType(), Name: current.GetName(), Schedule: current.GetCronExpression(),
			Timezone: current.GetCronTimezone(), Input: current.GetInputTemplate(), Event: current.GetEventType(),
			SourceAgentID: current.GetEventSourceAgentId(),
		}
		if !current.GetEnabled() {
			enabled := false
			result.Enabled = &enabled
		}
		return result
	}

	result := *source
	effective := effectivePulledTrigger(*source, agentName, index)
	if current.GetName() != effective.Name {
		result.Name = current.GetName()
	}
	if current.GetTriggerType() != effective.Type {
		result.Type = current.GetTriggerType()
	}
	if current.GetCronExpression() != effective.Schedule {
		result.Schedule = current.GetCronExpression()
	}
	if current.GetCronTimezone() != effective.Timezone {
		result.Timezone = current.GetCronTimezone()
	}
	if current.GetInputTemplate() != effective.Input {
		result.Input = current.GetInputTemplate()
	}
	if current.GetEventType() != effective.Event {
		result.Event = current.GetEventType()
	}
	if current.GetEventSourceAgentId() != effective.SourceAgentID {
		result.SourceAgentID = current.GetEventSourceAgentId()
	}
	if current.GetEnabled() != effective.IsEnabled() {
		enabled := current.GetEnabled()
		result.Enabled = &enabled
	}
	return result
}

func currentRevisionSubagentsForPull(
	ctx context.Context,
	f *client.Factory,
	agent *agentsv1.AgentDefinition,
	manifest *agentrevision.Manifest,
) ([]revisionPulledSubagent, []string, error) {
	declaredPaths, err := pulledSubagentPaths(manifest)
	if err != nil {
		return nil, nil, err
	}
	linkResponse, err := f.Agents().ListAgentLinks(ctx, connect.NewRequest(&agentsv1.ListAgentLinksRequest{AgentId: agent.GetId()}))
	if err != nil {
		return nil, nil, client.MapError(err)
	}
	usedPaths := make(map[string]struct{}, len(declaredPaths))
	result := make([]revisionPulledSubagent, 0)
	for _, link := range linkResponse.Msg.GetLinks() {
		if link.GetLinkType() != agentsv1.AgentLinkType_AGENT_LINK_TYPE_SUBORDINATE || link.GetTargetType() != agentLinkTargetAgent {
			continue
		}
		response, err := f.Agents().GetAgent(ctx, connect.NewRequest(&agentsv1.GetAgentRequest{Id: link.GetTargetId()}))
		if err != nil {
			if connectErrorHasCode(err, connect.CodeNotFound) {
				return nil, nil, fmt.Errorf("subordinate link %q targets missing agent %q", link.GetId(), link.GetTargetId())
			}
			return nil, nil, client.MapError(err)
		}
		subagent := response.Msg.GetAgent()
		if subagent == nil {
			return nil, nil, fmt.Errorf("subordinate link %q returned an empty agent for target %q", link.GetId(), link.GetTargetId())
		}
		if subagent.GetMode() != agentsv1.AgentMode_AGENT_MODE_SUBAGENT {
			return nil, nil, fmt.Errorf("subordinate link %q targets agent %q without subagent mode", link.GetId(), link.GetTargetId())
		}
		subPath, err := matchPulledSubagentPath(declaredPaths, usedPaths, subagent.GetName(), stampedSubagentPath(agent, subagent.GetName()))
		if err != nil {
			return nil, nil, fmt.Errorf("subagent %s: %w", subagent.GetName(), err)
		}
		usedPaths[subPath] = struct{}{}
		result = append(result, revisionPulledSubagent{agent: subagent, path: subPath})
	}
	order := make(map[string]int, len(declaredPaths))
	for i, path := range declaredPaths {
		order[path] = i
	}
	sort.SliceStable(result, func(i, j int) bool {
		left, leftKnown := order[result[i].path]
		right, rightKnown := order[result[j].path]
		if leftKnown != rightKnown {
			return leftKnown
		}
		if leftKnown {
			return left < right
		}
		return result[i].path < result[j].path
	})
	declarations := make([]string, len(result))
	for i, subagent := range result {
		declarations[i] = "./" + subagent.path
	}
	return result, declarations, nil
}

// overlayRevisionDefinition keeps the revision-owned source tree intact while
// projecting fields that can be edited in the dashboard back into agent.yaml
// and its instructions file. Pull therefore gives the user a deployable view
// of the current agent instead of silently restoring stale definition values.
func overlayRevisionDefinition(
	manifest *agentrevision.Manifest,
	source agentproject.Config,
	agent *agentsv1.AgentDefinition,
	triggers []agentproject.Trigger,
	subagents []string,
) ([]agentrevision.File, error) {
	files := make([]agentrevision.File, len(manifest.Files))
	agentYAMLIndex := -1
	for i, file := range manifest.Files {
		files[i] = file
		files[i].Content = append([]byte(nil), file.Content...)
		if file.Path == "agent.yaml" {
			agentYAMLIndex = i
		}
	}
	if agentYAMLIndex < 0 {
		return nil, fmt.Errorf("active revision does not contain agent.yaml")
	}

	var document map[string]any
	if err := yaml.Unmarshal(files[agentYAMLIndex].Content, &document); err != nil {
		return nil, fmt.Errorf("active revision agent.yaml: %w", err)
	}
	if document == nil {
		return nil, fmt.Errorf("active revision agent.yaml must be a YAML mapping")
	}
	instructionsPath, _ := document["instructions"].(string)
	if instructionsPath == "" {
		instructionsPath = "instructions.md"
	}
	instructionsPath, err := normalizePulledProjectPath(instructionsPath)
	if err != nil {
		return nil, fmt.Errorf("active revision instructions path: %w", err)
	}
	instructionsIndex := -1
	for i := range files {
		if files[i].Path == instructionsPath {
			instructionsIndex = i
			break
		}
	}
	if instructionsIndex < 0 {
		return nil, fmt.Errorf("active revision instructions file %q is missing", instructionsPath)
	}

	agentYAMLChanged := false
	setField := func(name string, value any) {
		document[name] = value
		agentYAMLChanged = true
	}
	deleteField := func(name string) {
		delete(document, name)
		agentYAMLChanged = true
	}
	if source.Name != agent.GetName() {
		setField("name", agent.GetName())
	}
	if source.Description != agent.GetDescription() {
		setField("description", agent.GetDescription())
	}
	if source.Model != agent.GetModel() {
		setField("model", agent.GetModel())
	}
	limits := agentproject.Limits{
		MaxTurns: int(agent.GetMaxTurns()), MaxToolCallsPerTurn: int(agent.GetMaxToolCallsPerTurn()), MaxSteps: int(agent.GetMaxSteps()),
	}
	if source.Limits != limits {
		if limits == (agentproject.Limits{}) {
			deleteField("limits")
		} else {
			setField("limits", limits)
		}
	}
	sourceTaskMode := source.Permissions.TaskMode
	if sourceTaskMode == "" {
		sourceTaskMode = "ask"
	}
	currentTaskMode := taskModeFromProto(agent.GetTaskPermissionMode())
	if currentTaskMode == "" {
		currentTaskMode = "ask"
	}
	if sourceTaskMode != currentTaskMode {
		setField("permissions", agentproject.Permissions{TaskMode: currentTaskMode})
	}

	config := pulledUserConfig(agent, source.Config, len(manifest.Functions) > 0)
	if !pulledConfigsEqual(source.Config, config) {
		if len(config) == 0 {
			deleteField("config")
		} else {
			setField("config", config)
		}
	}
	tools := pulledTools(source.Tools, manifest, agent.GetTools())
	if !reflect.DeepEqual(source.Tools, tools) {
		if len(tools) == 0 {
			deleteField("tools")
		} else {
			setField("tools", tools)
		}
	}
	if !reflect.DeepEqual(source.Triggers, triggers) {
		if len(triggers) == 0 {
			deleteField("triggers")
		} else {
			setField("triggers", triggers)
		}
	}
	if !pulledProjectPathsEqual(source.Subagents, subagents) {
		if len(subagents) == 0 {
			deleteField("subagents")
		} else {
			setField("subagents", subagents)
		}
	}

	if agentYAMLChanged {
		agentYAML, err := yaml.Marshal(document)
		if err != nil {
			return nil, fmt.Errorf("render dashboard agent definition: %w", err)
		}
		files[agentYAMLIndex].Content = agentYAML
	}
	if strings.TrimSpace(string(files[instructionsIndex].Content)) != agent.GetSystemPrompt() {
		files[instructionsIndex].Content = []byte(agent.GetSystemPrompt() + "\n")
	}
	return files, nil
}

func pulledUserConfig(agent *agentsv1.AgentDefinition, source map[string]any, hasFunctions bool) map[string]any {
	config := agent.GetConfig().AsMap()
	delete(config, "skills")
	delete(config, projectMetaKey)
	if hasFunctions {
		currentSandbox, currentOK := config["sandbox"].(map[string]any)
		sourceSandbox, _ := source["sandbox"].(map[string]any)
		if currentOK {
			sandbox := make(map[string]any, len(currentSandbox))
			for key, value := range currentSandbox {
				sandbox[key] = value
			}
			if _, explicit := sourceSandbox["enabled"]; !explicit && sandbox["enabled"] == true {
				delete(sandbox, "enabled")
			}
			if _, explicit := sourceSandbox["network_mode"]; !explicit && sandbox["network_mode"] == "deny" {
				delete(sandbox, "network_mode")
			}
			if len(sandbox) == 0 {
				delete(config, "sandbox")
			} else {
				config["sandbox"] = sandbox
			}
		}
	}
	if len(config) == 0 {
		return nil
	}
	return config
}

func pulledConfigsEqual(left, right map[string]any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func pulledTools(source []string, manifest *agentrevision.Manifest, current []string) []string {
	localFunctions := make(map[string]struct{}, len(manifest.Functions))
	for _, function := range manifest.Functions {
		localFunctions[function.Name] = struct{}{}
	}
	result := make([]string, 0, len(source)+len(current))
	for _, tool := range current {
		if _, local := localFunctions[tool]; !local {
			result = append(result, tool)
		}
	}
	for _, tool := range source {
		if strings.HasPrefix(tool, "./") || strings.HasPrefix(tool, "tools/") {
			result = append(result, tool)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func pulledProjectPathsEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		leftPath, leftErr := normalizePulledProjectPath(left[i])
		rightPath, rightErr := normalizePulledProjectPath(right[i])
		if leftErr != nil || rightErr != nil || leftPath != rightPath {
			return false
		}
	}
	return true
}

func validatePulledRevision(agent *agentsv1.AgentDefinition, remote *agentsv1.AgentRevision) (*agentrevision.Manifest, error) {
	if remote.GetAgentId() != "" && remote.GetAgentId() != agent.GetId() {
		return nil, fmt.Errorf("active revision %q belongs to agent %q, not %q", remote.GetId(), remote.GetAgentId(), agent.GetId())
	}
	if remote.GetFormat() != agentrevision.FormatV1 {
		return nil, fmt.Errorf("active revision %q uses unsupported format %d", remote.GetId(), remote.GetFormat())
	}
	files := make([]agentrevision.File, 0, len(remote.GetFiles()))
	for _, file := range remote.GetFiles() {
		if file == nil {
			continue
		}
		files = append(files, agentrevision.File{
			Path: file.GetPath(), Content: append([]byte(nil), file.GetContent()...), Mode: file.GetMode(),
		})
	}
	functions := make([]agentrevision.Function, 0, len(remote.GetFunctions()))
	for _, function := range remote.GetFunctions() {
		if function == nil {
			continue
		}
		var parameters map[string]any
		if function.GetParameters() != nil {
			parameters = function.GetParameters().AsMap()
		}
		functions = append(functions, agentrevision.Function{
			Name: function.GetName(), Description: function.GetDescription(), Path: function.GetPath(),
			Export: function.GetExportName(), Runtime: isolation.Runtime(function.GetRuntime()), Parameters: parameters,
		})
	}
	manifest, err := agentrevision.NewManifest(files, functions)
	if err != nil {
		return nil, fmt.Errorf("active revision %q is invalid: %w", remote.GetId(), err)
	}
	if manifest.Digest != remote.GetDigest() {
		return nil, fmt.Errorf("active revision %q failed integrity validation: digest is %s, computed %s", remote.GetId(), remote.GetDigest(), manifest.Digest)
	}
	remoteFiles := make(map[string]*agentsv1.AgentRevisionFile, len(remote.GetFiles()))
	for _, file := range remote.GetFiles() {
		if file != nil {
			remoteFiles[file.GetPath()] = file
		}
	}
	if len(remoteFiles) != len(manifest.Files) {
		return nil, fmt.Errorf("active revision %q file metadata failed integrity validation", remote.GetId())
	}
	for _, file := range manifest.Files {
		remoteFile := remoteFiles[file.Path]
		if remoteFile == nil {
			return nil, fmt.Errorf("active revision %q file %q is missing metadata", remote.GetId(), file.Path)
		}
		if remoteFile.GetPath() != file.Path || remoteFile.GetSha256() != file.SHA256 || remoteFile.GetSizeBytes() != file.Size {
			return nil, fmt.Errorf("active revision %q file metadata failed integrity validation", remote.GetId())
		}
	}
	return manifest, nil
}

func pulledSubagentPaths(manifest *agentrevision.Manifest) ([]string, error) {
	var agentYAML []byte
	for _, file := range manifest.Files {
		if file.Path == "agent.yaml" {
			agentYAML = file.Content
			break
		}
	}
	if len(agentYAML) == 0 {
		return nil, fmt.Errorf("active revision does not contain agent.yaml")
	}
	var source struct {
		Subagents []string `yaml:"subagents"`
	}
	if err := yaml.Unmarshal(agentYAML, &source); err != nil {
		return nil, fmt.Errorf("active revision agent.yaml: %w", err)
	}
	paths := make([]string, 0, len(source.Subagents))
	for _, declared := range source.Subagents {
		normalized, err := normalizePulledProjectPath(declared)
		if err != nil {
			return nil, fmt.Errorf("active revision subagent path %q: %w", declared, err)
		}
		paths = append(paths, normalized)
	}
	return paths, nil
}

func normalizePulledProjectPath(raw string) (string, error) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "./")
	if raw == "" || strings.Contains(raw, `\`) || filepath.IsAbs(raw) || filepath.VolumeName(raw) != "" {
		return "", fmt.Errorf("must be a relative project path")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(raw)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("must stay within the project")
	}
	return clean, nil
}

func matchPulledSubagentPath(paths []string, used map[string]struct{}, name, stampedPath string) (string, error) {
	if stampedPath != "" {
		normalized, err := normalizePulledProjectPath(stampedPath)
		if err != nil {
			return "", fmt.Errorf("deployment stamp has invalid subagent path: %w", err)
		}
		for _, candidate := range paths {
			if candidate == normalized {
				if _, exists := used[candidate]; exists {
					return "", fmt.Errorf("deployment stamp maps more than one agent to %q", candidate)
				}
				return candidate, nil
			}
		}
		if _, exists := used[normalized]; exists {
			return "", fmt.Errorf("deployment stamp maps more than one agent to %q", normalized)
		}
		return normalized, nil
	}
	for _, candidate := range paths {
		if _, exists := used[candidate]; !exists && filepath.Base(filepath.FromSlash(candidate)) == name {
			return candidate, nil
		}
	}
	var remaining []string
	for _, candidate := range paths {
		if _, exists := used[candidate]; !exists {
			remaining = append(remaining, candidate)
		}
	}
	if len(remaining) == 1 {
		return remaining[0], nil
	}
	if len(remaining) == 0 {
		if err := validateRemotePathSegment(name, "subagent name"); err != nil {
			return "", err
		}
		candidate := "subagents/" + name
		if _, exists := used[candidate]; exists {
			return "", fmt.Errorf("cannot assign unique project path for linked agent %q", name)
		}
		return candidate, nil
	}
	return "", fmt.Errorf("cannot map linked agent to a unique path declared by agent.yaml")
}

func stampedSubagentPath(agent *agentsv1.AgentDefinition, name string) string {
	if agent == nil || agent.GetConfig() == nil {
		return ""
	}
	config := agent.GetConfig().AsMap()
	meta, _ := config[projectMetaKey].(map[string]any)
	paths, _ := meta["subagent_paths"].(map[string]any)
	value, _ := paths[name].(string)
	return value
}

func isPulledSkillFile(filePath string) bool {
	parts := strings.Split(filePath, "/")
	return len(parts) == 3 && parts[0] == "skills" && parts[2] == "SKILL.md"
}

func fetchPulledTool(ctx context.Context, f *client.Factory, tool string) (string, map[string][]byte, bool, error) {
	resp, err := f.Functions().GetFunctionByName(ctx, connect.NewRequest(&functionsv1.GetFunctionByNameRequest{Name: tool}))
	if err != nil {
		if connectErrorHasCode(err, connect.CodeNotFound) {
			return "", nil, false, nil
		}
		return "", nil, false, client.MapError(err)
	}
	fn := resp.Msg.GetFunction()
	isolated := fn.GetIsolated()
	if fn.GetMode() != functionsv1.ExecutionMode_EXECUTION_MODE_ISOLATED || isolated.GetCode() == "" {
		return "", nil, false, nil
	}
	if err := agentproject.ValidateToolName(tool); err != nil {
		return "", nil, false, fmt.Errorf("function %q cannot be represented as a local tool: %w", tool, err)
	}

	var extension string
	switch isolated.GetRuntime() {
	case "deno":
		extension = ".ts"
	case "python3":
		extension = ".py"
	case "nodejs20":
		extension = ".js"
	default:
		return "", nil, false, fmt.Errorf("function %q uses unsupported runtime %q; pull supports deno, python3 and nodejs20", tool, isolated.GetRuntime())
	}

	filename := tool + extension
	files := map[string][]byte{filepath.ToSlash(filepath.Join("tools", filename)): []byte(isolated.GetCode())}
	if parameters := fn.GetParameters(); parameters != nil && len(parameters.GetFields()) > 0 {
		data, err := json.MarshalIndent(parameters.AsMap(), "", "  ")
		if err != nil {
			return "", nil, false, fmt.Errorf("function %q parameters: %w", tool, err)
		}
		files[filepath.ToSlash(filepath.Join("tools", tool+".params.json"))] = append(data, '\n')
	}
	return "./tools/" + filename, files, true, nil
}

func renderPulledSkill(name, description, content string) ([]byte, error) {
	frontmatter, err := yaml.Marshal(struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description,omitempty"`
	}{Name: name, Description: description})
	if err != nil {
		return nil, err
	}
	body := stripSkillFrontmatter(content)
	return []byte("---\n" + string(frontmatter) + "---\n\n" + strings.TrimLeft(body, "\r\n")), nil
}

func stripSkillFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---\n") {
		return content
	}
	rest := content[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return content
	}
	bodyStart := 4 + end + len("\n---")
	if bodyStart < len(content) && content[bodyStart] == '\r' {
		bodyStart++
	}
	if bodyStart < len(content) && content[bodyStart] == '\n' {
		bodyStart++
	}
	return content[bodyStart:]
}

func writePullBundle(root string, bundle *pullBundle) error {
	paths := make([]string, 0, len(bundle.files))
	for path := range bundle.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		file := bundle.files[path]
		if err := writeProjectFile(root, file.data, file.mode, strings.Split(filepath.ToSlash(path), "/")...); err != nil {
			return err
		}
	}
	return nil
}

func validateRemotePathSegment(value, label string) error {
	if value == "" || strings.TrimSpace(value) != value || value == "." || value == ".." ||
		strings.ContainsAny(value, `/\\`) || filepath.IsAbs(value) || filepath.VolumeName(value) != "" ||
		filepath.Base(value) != value {
		return fmt.Errorf("unsafe %s %q: expected a single path-safe name", label, value)
	}
	return nil
}

func canonicalOutputRoot(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", dir)
	}
	return root, nil
}

func secureProjectPath(root string, elems ...string) (string, error) {
	candidate := filepath.Join(append([]string{root}, elems...)...)
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("output path must stay within %s", root)
	}

	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refusing output path through symlink %s", current)
		}
	}
	return candidate, nil
}

func secureMkdirAll(root string, elems ...string) (string, error) {
	current := root
	for _, elem := range elems {
		if err := validateRemotePathSegment(elem, "output path segment"); err != nil {
			return "", err
		}
		next, err := secureProjectPath(root, append(strings.Split(strings.TrimPrefix(current, root), string(filepath.Separator)), elem)...)
		if err != nil {
			return "", err
		}
		if err := os.Mkdir(next, 0o755); err != nil && !os.IsExist(err) {
			return "", err
		}
		info, err := os.Lstat(next)
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refusing non-directory output path %s", next)
		}
		current = next
	}
	return current, nil
}

func writeProjectFile(root string, data []byte, mode os.FileMode, elems ...string) error {
	if len(elems) == 0 {
		return fmt.Errorf("output file path is empty")
	}
	parent, err := secureMkdirAll(root, elems[:len(elems)-1]...)
	if err != nil {
		return err
	}
	target, err := secureProjectPath(root, elems...)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to overwrite symlink %s", target)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	tmp, err := os.CreateTemp(parent, ".evs-pull-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if mode == 0 {
		mode = 0o644
	}
	if err := tmp.Chmod(mode.Perm()); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, target)
}
