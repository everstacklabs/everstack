package cmd

import (
	"io"
	"strings"
	"testing"
)

func TestRootRegistersProjectDeploymentCommands(t *testing.T) {
	root := New(io.Discard, strings.NewReader(""), nil, nil)

	for _, name := range []string{"agents", "init", "deploy"} {
		t.Run(name, func(t *testing.T) {
			command, _, err := root.Find([]string{name})
			if err != nil {
				t.Fatalf("Find(%q): %v", name, err)
			}
			if command == root || command.Name() != name {
				t.Fatalf("Find(%q) returned %q", name, command.CommandPath())
			}
		})
	}
}
