package agents

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/everstacklabs/everstack/internal/cli/agentproject"
)

func TestInitScaffoldQuotesNameAndOnlyAdvertisesAvailableTools(t *testing.T) {
	name := "support: #1\nwest"
	dir := t.TempDir()
	for path, content := range map[string]string{
		"agent.yaml":            initAgentYAML(name),
		"instructions.md":       initInstructions,
		"src/get_time.ts":       initTool,
		"skills/style/SKILL.md": initSkill,
	} {
		target := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	project, err := agentproject.Load(dir)
	if err != nil {
		t.Fatalf("Load(scaffold) error = %v", err)
	}
	if project.Config.Name != name {
		t.Fatalf("scaffold name = %q, want %q", project.Config.Name, name)
	}
	if !reflect.DeepEqual(project.BuiltinTools, []string{"web_search"}) {
		t.Fatalf("scaffold builtin tools = %v, want only implemented web_search", project.BuiltinTools)
	}
	if len(project.ToolFiles) != 1 || project.ToolFiles[0].Name != "get_time" || project.ToolFiles[0].Export != "getTime" {
		t.Fatalf("scaffold project functions = %+v", project.ToolFiles)
	}
}
