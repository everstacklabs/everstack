package agentproject

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func scaffold(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const validYAML = `name: support-bot
description: Test agent
model: claude-sonnet-5
limits:
  max_turns: 30
permissions:
  task_mode: ask
config:
  temperature: 0.2
tools:
  - web_search
  - ./tools/lookup.ts
  - ./tools/refund.py
skills:
  - ./skills/billing
triggers:
  - type: cron
    name: daily
    schedule: "0 9 * * *"
    input: "Daily summary"
  - type: webhook
    name: inbound
`

func validProject(t *testing.T) string {
	return scaffold(t, map[string]string{
		"agent.yaml":              validYAML,
		"instructions.md":         "# Role\nBe helpful.",
		"tools/lookup.ts":         "export default async (args) => args;",
		"tools/refund.py":         "def handler(args):\n    return args\n",
		"skills/billing/SKILL.md": "---\nname: billing\ndescription: Billing playbook\n---\n\n# Billing\nRefund policy...",
	})
}

func TestLoadValidProject(t *testing.T) {
	p, err := Load(validProject(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Config.Name != "support-bot" || p.Config.Model != "claude-sonnet-5" {
		t.Errorf("config parsed wrong: %+v", p.Config)
	}
	if p.Instructions != "# Role\nBe helpful." {
		t.Errorf("instructions = %q", p.Instructions)
	}
	if len(p.BuiltinTools) != 1 || p.BuiltinTools[0] != "web_search" {
		t.Errorf("builtins = %v", p.BuiltinTools)
	}
	if len(p.ToolFiles) != 2 {
		t.Fatalf("tool files = %d", len(p.ToolFiles))
	}
	if p.ToolFiles[0].Name != "lookup" || p.ToolFiles[0].Runtime != "deno" {
		t.Errorf("ts tool: %+v", p.ToolFiles[0])
	}
	if p.ToolFiles[1].Name != "refund" || p.ToolFiles[1].Runtime != "python3" {
		t.Errorf("py tool: %+v", p.ToolFiles[1])
	}
	if len(p.Skills) != 1 || p.Skills[0].Name != "billing" || p.Skills[0].Description != "Billing playbook" {
		t.Errorf("skills = %+v", p.Skills)
	}
	if len(p.Config.Triggers) != 2 {
		t.Errorf("triggers = %+v", p.Config.Triggers)
	}
	for _, trigger := range p.Config.Triggers {
		if trigger.Timezone != "UTC" || !trigger.IsEnabled() {
			t.Errorf("trigger defaults = %+v, want timezone UTC and enabled", trigger)
		}
	}
}

func TestLoadAllowsEmptyInstructions(t *testing.T) {
	dir := scaffold(t, map[string]string{
		"agent.yaml":      "name: no-prompt\nmodel: model\n",
		"instructions.md": "",
	})
	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if p.Instructions != "" {
		t.Fatalf("Instructions = %q, want empty", p.Instructions)
	}
}

func TestLoadRejectsProjectFunctionThatShadowsRuntimeTool(t *testing.T) {
	dir := scaffold(t, map[string]string{
		"agent.yaml": `name: collision
model: model
functions:
  sandbox_execute:
    file: ./run.py
`,
		"instructions.md": "Run safely.",
		"run.py":          "def handler(args):\n    return args\n",
	})

	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "reserved by Agent Runtime") {
		t.Fatalf("Load() error = %v, want reserved runtime tool error", err)
	}
}

func TestLoadPreservesDisabledTrigger(t *testing.T) {
	dir := scaffold(t, map[string]string{
		"agent.yaml":      "name: a\nmodel: m\ntriggers:\n  - type: webhook\n    name: paused\n    enabled: false\n",
		"instructions.md": "x",
	})
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Config.Triggers) != 1 || p.Config.Triggers[0].IsEnabled() {
		t.Fatalf("triggers = %+v, want one disabled trigger", p.Config.Triggers)
	}
}

func TestHashStability(t *testing.T) {
	dir := validProject(t)
	p1, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p1.Hash() != p2.Hash() {
		t.Error("hash not stable across loads")
	}

	// Changing a tool file changes the hash.
	if err := os.WriteFile(filepath.Join(dir, "tools", "lookup.ts"), []byte("export default async () => 42;"), 0o644); err != nil {
		t.Fatal(err)
	}
	p3, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p3.Hash() == p1.Hash() {
		t.Error("hash did not change when tool code changed")
	}
}

func TestHashIncludesCompiledConfigAndToolParameters(t *testing.T) {
	dir := validProject(t)
	paramsPath := filepath.Join(dir, "tools", "lookup.params.json")
	if err := os.WriteFile(paramsPath, []byte(`{"type":"object","properties":{"query":{"type":"string"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	baseline, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	updatedYAML := strings.Replace(validYAML, "temperature: 0.2", "temperature: 0.7", 1)
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(updatedYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	configChanged, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Hash() == configChanged.Hash() {
		t.Error("hash did not change when compiled agent config changed")
	}

	if err := os.WriteFile(paramsPath, []byte(`{"type":"object","required":["query"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	parametersChanged, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if configChanged.Hash() == parametersChanged.Hash() {
		t.Error("hash did not change when a tool parameter schema changed")
	}
}

func TestHashUsesUnambiguousCanonicalState(t *testing.T) {
	base := Project{Config: Config{Name: "agent", Model: "model"}, Instructions: "instructions"}
	first := base
	first.Config.Limits = Limits{MaxTurns: 1, MaxToolCallsPerTurn: 23, MaxSteps: 4}
	second := base
	second.Config.Limits = Limits{MaxTurns: 12, MaxToolCallsPerTurn: 3, MaxSteps: 4}

	if first.Hash() == second.Hash() {
		t.Error("different limit tuples produced the same hash")
	}

	left := base
	left.Config.Config = map[string]any{
		"temperature": 0.2,
		"nested":      map[string]any{"enabled": true, "limit": 3},
	}
	right := base
	right.Config.Config = map[string]any{
		"nested":      map[string]any{"limit": 3, "enabled": true},
		"temperature": 0.2,
	}
	if left.Hash() != right.Hash() {
		t.Error("map insertion order changed the canonical hash")
	}
}

func TestHashIgnoresTriggerDeclarationOrder(t *testing.T) {
	enabled := true
	base := Project{Config: Config{Name: "agent", Model: "model"}, Instructions: "instructions"}
	base.Config.Triggers = []Trigger{
		{Name: "z-last", Type: "webhook", Timezone: "UTC", Enabled: &enabled},
		{Name: "a-first", Type: "cron", Schedule: "0 9 * * *", Timezone: "UTC", Enabled: &enabled},
	}
	reordered := base
	reordered.Config.Triggers = append([]Trigger(nil), base.Config.Triggers...)
	reordered.Config.Triggers[0], reordered.Config.Triggers[1] = reordered.Config.Triggers[1], reordered.Config.Triggers[0]
	if base.Hash() != reordered.Hash() {
		t.Fatal("trigger declaration order changed the project hash")
	}
}

func TestLoadRejectsInvalid(t *testing.T) {
	cases := map[string]map[string]string{
		"missing name": {
			"agent.yaml":      "model: m\n",
			"instructions.md": "x",
		},
		"missing model": {
			"agent.yaml":      "name: a\n",
			"instructions.md": "x",
		},
		"bad task mode": {
			"agent.yaml":      "name: a\nmodel: m\npermissions:\n  task_mode: sometimes\n",
			"instructions.md": "x",
		},
		"missing instructions": {
			"agent.yaml": "name: a\nmodel: m\n",
		},
		"unknown yaml key": {
			"agent.yaml":      "name: a\nmodel: m\ninstruction: typo.md\n",
			"instructions.md": "x",
		},
		"cron without schedule": {
			"agent.yaml":      "name: a\nmodel: m\ntriggers:\n  - type: cron\n",
			"instructions.md": "x",
		},
		"cron with invalid schedule": {
			"agent.yaml":      "name: a\nmodel: m\ntriggers:\n  - type: cron\n    schedule: not-a-cron\n",
			"instructions.md": "x",
		},
		"cron with invalid timezone": {
			"agent.yaml":      "name: a\nmodel: m\ntriggers:\n  - type: cron\n    schedule: 0 9 * * *\n    timezone: Mars/Olympus\n",
			"instructions.md": "x",
		},
		"event without source": {
			"agent.yaml":      "name: a\nmodel: m\ntriggers:\n  - type: event\n    name: completed\n    event: completed\n",
			"instructions.md": "x",
		},
		"bad tool extension": {
			"agent.yaml":      "name: a\nmodel: m\ntools:\n  - ./tools/x.rb\n",
			"instructions.md": "x",
			"tools/x.rb":      "puts 1",
		},
		"config is not JSON compatible": {
			"agent.yaml":      "name: a\nmodel: m\nconfig:\n  temperature: .nan\n",
			"instructions.md": "x",
		},
		"negative limit": {
			"agent.yaml":      "name: a\nmodel: m\nlimits:\n  max_turns: -1\n",
			"instructions.md": "x",
		},
		"limit exceeds API range": {
			"agent.yaml":      "name: a\nmodel: m\nlimits:\n  max_steps: 2147483648\n",
			"instructions.md": "x",
		},
		"duplicate builtin tool": {
			"agent.yaml":      "name: a\nmodel: m\ntools:\n  - web_search\n  - web_search\n",
			"instructions.md": "x",
		},
		"builtin and local tool collide": {
			"agent.yaml":      "name: a\nmodel: m\ntools:\n  - lookup\n  - ./tools/lookup.ts\n",
			"instructions.md": "x",
			"tools/lookup.ts": "export default () => null",
		},
		"duplicate skill name": {
			"agent.yaml":          "name: a\nmodel: m\nskills:\n  - ./skills/one\n  - ./skills/two\n",
			"instructions.md":     "x",
			"skills/one/SKILL.md": "---\nname: shared\n---\nOne",
			"skills/two/SKILL.md": "---\nname: shared\n---\nTwo",
		},
		"duplicate effective trigger name": {
			"agent.yaml":      "name: a\nmodel: m\ntriggers:\n  - type: webhook\n    name: duplicate\n  - type: event\n    name: duplicate\n    event: completed\n    source_agent_id: source-id\n",
			"instructions.md": "x",
		},
	}
	for label, files := range cases {
		if _, err := Load(scaffold(t, files)); err == nil {
			t.Errorf("%s: expected error", label)
		}
	}
}

func TestLoadScopesFunctionNamesToEachAgentRevision(t *testing.T) {
	dir := scaffold(t, map[string]string{
		"agent.yaml":                      "name: parent\nmodel: m\ntools:\n  - ./tools/shared.ts\nsubagents:\n  - ./subagents/child\n",
		"instructions.md":                 "x",
		"tools/shared.ts":                 "export default () => 'parent'",
		"subagents/child/agent.yaml":      "name: child\nmodel: m\ntools:\n  - ./tools/shared.py\n",
		"subagents/child/instructions.md": "x",
		"subagents/child/tools/shared.py": "def handler(args):\n    return 'child'\n",
	})
	project, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() rejected revision-scoped function names: %v", err)
	}
	if len(project.ToolFiles) != 1 || len(project.Subagents) != 1 || len(project.Subagents[0].ToolFiles) != 1 {
		t.Fatalf("unexpected project graph: %+v", project)
	}
	if project.RevisionManifest.Digest == project.Subagents[0].RevisionManifest.Digest {
		t.Fatal("parent and subagent revisions unexpectedly share a digest")
	}
}

func TestLoadExplicitProjectFunctionsAndSourceBundle(t *testing.T) {
	dir := scaffold(t, map[string]string{
		"agent.yaml": `name: code-agent
model: model
functions:
  lookup_customer:
    file: ./abc.ts
    export: lookupCustomer
    description: Find a customer
    parameters:
      type: object
      required: [email]
  score_risk:
    file: ./lib/risk.py
    export: score
files:
  - ./lib
  - ./README.md
  - ./.env.example
`,
		"instructions.md":         "Run project functions.",
		"abc.ts":                  "import { normalize } from './lib/customer.ts';\nexport const lookupCustomer = ({email}) => normalize(email);\n",
		"lib/customer.ts":         "export const normalize = (value) => value.trim().toLowerCase();\n",
		"lib/risk.py":             "def score(args):\n    return {'risk': args.get('value', 0)}\n",
		"README.md":               "# Code agent\n",
		".env":                    "DO_NOT_UPLOAD=secret\n",
		".env.production":         "DO_NOT_UPLOAD=secret\n",
		".env.example":            "API_KEY=replace-me\n",
		".npmrc":                  "//registry.npmjs.org/:_authToken=secret\n",
		"credentials.json":        `{"token":"secret"}`,
		"identity.KEY":            "secret",
		".everstack/args.json":    `{"token":"runtime-secret"}`,
		".ssh/id_ed25519":         "secret",
		"node_modules/x/index.js": "throw new Error('do not bundle dependencies')\n",
	})

	project, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(project.ToolFiles) != 2 {
		t.Fatalf("project functions = %+v, want two", project.ToolFiles)
	}
	if project.ToolFiles[0].Name != "lookup_customer" || project.ToolFiles[0].Export != "lookupCustomer" {
		t.Fatalf("lookup function = %+v", project.ToolFiles[0])
	}
	if project.RevisionManifest == nil || len(project.RevisionManifest.Digest) != 64 {
		t.Fatalf("revision manifest = %+v", project.RevisionManifest)
	}
	paths := map[string]bool{}
	for _, file := range project.RevisionManifest.Files {
		paths[file.Path] = true
	}
	for _, required := range []string{"agent.yaml", "instructions.md", "abc.ts", "lib/customer.ts", "lib/risk.py", "README.md", ".env.example"} {
		if !paths[required] {
			t.Errorf("revision is missing %s", required)
		}
	}
	for _, excluded := range []string{
		".env", ".env.production", ".npmrc", "credentials.json", "identity.KEY",
		".everstack/args.json", ".ssh/id_ed25519", "node_modules/x/index.js",
	} {
		if paths[excluded] {
			t.Fatalf("revision included excluded file %q: %+v", excluded, paths)
		}
	}
}

func TestHashChangesWhenImportedProjectSourceChanges(t *testing.T) {
	dir := scaffold(t, map[string]string{
		"agent.yaml":      "name: code-agent\nmodel: model\nfunctions:\n  calculate:\n    file: ./main.ts\n    export: calculate\nfiles:\n  - ./value.ts\n",
		"instructions.md": "x",
		"main.ts":         "import { value } from './value.ts'; export const calculate = () => value;\n",
		"value.ts":        "export const value = 1;\n",
	})
	before, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "value.ts"), []byte("export const value = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if before.Hash() == after.Hash() || before.RevisionManifest.Digest == after.RevisionManifest.Digest {
		t.Fatal("imported source change did not change the agent revision")
	}
}

func TestLoadOnlyBundlesRequiredAndExplicitSource(t *testing.T) {
	dir := scaffold(t, map[string]string{
		"agent.yaml":        "name: code-agent\nmodel: model\nfunctions:\n  calculate:\n    file: ./src/main.ts\n    export: calculate\nfiles:\n  - ./src\n",
		"instructions.md":   "x",
		"src/main.ts":       "import { value } from './value.ts'; export const calculate = () => value;\n",
		"src/value.ts":      "export const value = 1;\n",
		"README.md":         "not explicitly bundled\n",
		"secrets.yaml":      "token: do-not-upload\n",
		"terraform.tfstate": `{"token":"do-not-upload"}`,
		"src/.env":          "TOKEN=do-not-upload\n",
		"src/secrets.json":  `{"token":"do-not-upload"}`,
		"src/secrets.yaml":  "token: do-not-upload\n",
		"src/app.tfstate":   `{"token":"do-not-upload"}`,
		"src/kubeconfig":    "token: do-not-upload\n",
		"src/.config/gcloud/application_default_credentials.json": `{"token":"do-not-upload"}`,
	})
	project, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	paths := make(map[string]bool)
	for _, file := range project.RevisionManifest.Files {
		paths[file.Path] = true
	}
	for _, required := range []string{"agent.yaml", "instructions.md", "src/main.ts", "src/value.ts"} {
		if !paths[required] {
			t.Errorf("revision is missing %q", required)
		}
	}
	for _, excluded := range []string{
		"README.md", "secrets.yaml", "terraform.tfstate", "src/.env", "src/secrets.json",
		"src/secrets.yaml", "src/app.tfstate", "src/kubeconfig",
		"src/.config/gcloud/application_default_credentials.json",
	} {
		if paths[excluded] {
			t.Errorf("revision unexpectedly included %q", excluded)
		}
	}
}

func TestLoadAllowsOneSharedFunctionOwnerWithGraphReferences(t *testing.T) {
	dir := scaffold(t, map[string]string{
		"agent.yaml":                      "name: parent\nmodel: m\ntools:\n  - ./tools/shared.ts\nsubagents:\n  - ./subagents/child\n",
		"instructions.md":                 "x",
		"tools/shared.ts":                 "export default () => 'shared'",
		"subagents/child/agent.yaml":      "name: child\nmodel: m\ntools:\n  - shared\n",
		"subagents/child/instructions.md": "x",
	})
	if _, err := Load(dir); err != nil {
		t.Fatalf("Load() rejected shared Function reference: %v", err)
	}
}

const parentWithSubagentYAML = `name: support-bot
model: claude-sonnet-5
tools:
  - web_search
subagents:
  - ./subagents/risk-reviewer
`

const riskReviewerYAML = `name: risk-reviewer
model: claude-haiku-4-5
tools:
  - ./tools/score.py
`

func projectWithSubagent(t *testing.T) string {
	return scaffold(t, map[string]string{
		"agent.yaml":                              parentWithSubagentYAML,
		"instructions.md":                         "Parent role.",
		"subagents/risk-reviewer/agent.yaml":      riskReviewerYAML,
		"subagents/risk-reviewer/instructions.md": "Assess risk.",
		"subagents/risk-reviewer/tools/score.py":  "def handler(args):\n    return args\n",
	})
}

func TestLoadSubagents(t *testing.T) {
	p, err := Load(projectWithSubagent(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(p.Subagents) != 1 {
		t.Fatalf("subagents = %d", len(p.Subagents))
	}
	sub := p.Subagents[0]
	if sub.Config.Name != "risk-reviewer" || sub.Config.Model != "claude-haiku-4-5" {
		t.Errorf("subagent config: %+v", sub.Config)
	}
	if sub.Instructions != "Assess risk." {
		t.Errorf("subagent instructions = %q", sub.Instructions)
	}
	// Paths inside a subagent resolve against the subagent directory.
	if len(sub.ToolFiles) != 1 || sub.ToolFiles[0].Name != "score" || sub.ToolFiles[0].Runtime != "python3" {
		t.Errorf("subagent tools = %+v", sub.ToolFiles)
	}
}

func TestSubagentChangeDriftsParentHash(t *testing.T) {
	dir := projectWithSubagent(t)
	p1, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "subagents", "risk-reviewer", "instructions.md")
	if err := os.WriteFile(path, []byte("Assess risk carefully."), 0o644); err != nil {
		t.Fatal(err)
	}
	p2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p1.Hash() == p2.Hash() {
		t.Error("parent hash unchanged after a subagent changed")
	}
}

func TestLoadRejectsBadSubagents(t *testing.T) {
	cases := map[string]map[string]string{
		"nested subagents exceed one level": {
			"agent.yaml":                                               parentWithSubagentYAML,
			"instructions.md":                                          "Parent role.",
			"subagents/risk-reviewer/agent.yaml":                       "name: risk-reviewer\nmodel: m\nsubagents:\n  - ./subagents/deeper\n",
			"subagents/risk-reviewer/instructions.md":                  "x",
			"subagents/risk-reviewer/subagents/deeper/agent.yaml":      "name: deeper\nmodel: m\n",
			"subagents/risk-reviewer/subagents/deeper/instructions.md": "x",
		},
		"subagent name collides with parent": {
			"agent.yaml":                     "name: support-bot\nmodel: m\nsubagents:\n  - ./subagents/dupe\n",
			"instructions.md":                "x",
			"subagents/dupe/agent.yaml":      "name: support-bot\nmodel: m\n",
			"subagents/dupe/instructions.md": "x",
		},
		"duplicate subagent names": {
			"agent.yaml":                  "name: parent\nmodel: m\nsubagents:\n  - ./subagents/a\n  - ./subagents/b\n",
			"instructions.md":             "x",
			"subagents/a/agent.yaml":      "name: same\nmodel: m\n",
			"subagents/a/instructions.md": "x",
			"subagents/b/agent.yaml":      "name: same\nmodel: m\n",
			"subagents/b/instructions.md": "x",
		},
		"missing subagent directory": {
			"agent.yaml":      "name: parent\nmodel: m\nsubagents:\n  - ./subagents/nope\n",
			"instructions.md": "x",
		},
	}
	for label, files := range cases {
		if _, err := Load(scaffold(t, files)); err == nil {
			t.Errorf("%s: expected error", label)
		}
	}
}

func TestValidateDeploymentAgentNamesRejectsDuplicateGraphNames(t *testing.T) {
	root := &Project{Dir: "/root", Config: Config{Name: "shared"}}
	root.Subagents = []*Project{{Dir: "/root/subagents/shared", Config: Config{Name: "shared"}}}
	if err := validateDeploymentAgentNames(root); err == nil || !strings.Contains(err.Error(), "agent name") {
		t.Fatalf("validateDeploymentAgentNames() error = %v, want duplicate agent name", err)
	}
}

func TestLoadRejectsPathsOutsideProject(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"instructions": "name: a\nmodel: m\ninstructions: ../outside.md\n",
		"tool":         "name: a\nmodel: m\ntools:\n  - ./tools/../../outside.ts\n",
		"skill":        "name: a\nmodel: m\nskills:\n  - ../outside-skill\n",
		"subagent":     "name: a\nmodel: m\nsubagents:\n  - ../outside-agent\n",
	}
	for name, config := range cases {
		name, config := name, config
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			parent := t.TempDir()
			project := filepath.Join(parent, "project")
			if err := os.MkdirAll(filepath.Join(project, "tools"), 0o755); err != nil {
				t.Fatal(err)
			}
			for path, content := range map[string]string{
				filepath.Join(project, "agent.yaml"):                      config,
				filepath.Join(project, "instructions.md"):                 "inside",
				filepath.Join(parent, "outside.md"):                       "outside",
				filepath.Join(parent, "outside.ts"):                       "export default () => null",
				filepath.Join(parent, "outside-skill", "SKILL.md"):        "outside",
				filepath.Join(parent, "outside-agent", "agent.yaml"):      "name: child\nmodel: m\n",
				filepath.Join(parent, "outside-agent", "instructions.md"): "outside",
			} {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			if _, err := Load(project); err == nil || !strings.Contains(err.Error(), "must stay within its project directory") {
				t.Fatalf("Load() error = %v, want project containment error", err)
			}
		})
	}
}

func TestLoadRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	project := filepath.Join(parent, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "agent.yaml"), []byte("name: a\nmodel: m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(project, "instructions.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := Load(project); err == nil || !strings.Contains(err.Error(), "must stay within its project directory") {
		t.Fatalf("Load() error = %v, want symlink containment error", err)
	}
}

func TestLoadReportsUnreadableToolParameterSidecar(t *testing.T) {
	t.Parallel()

	dir := scaffold(t, map[string]string{
		"agent.yaml":      "name: a\nmodel: m\ntools:\n  - ./tools/lookup.ts\n",
		"instructions.md": "inside",
		"tools/lookup.ts": "export default () => null",
	})
	if err := os.Mkdir(filepath.Join(dir, "tools", "lookup.params.json"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "params sidecar") {
		t.Fatalf("Load() error = %v, want params sidecar read error", err)
	}
}

func TestLoadMissingProject(t *testing.T) {
	_, err := Load(t.TempDir())
	if err == nil || !contains(err.Error(), "evs init") {
		t.Errorf("expected scaffold hint, got %v", err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
