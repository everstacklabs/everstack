package tools

import (
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

func TestShouldAutoDetachDevServer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{name: "npm dev", command: "npm run dev", want: true},
		{name: "npm dev with args", command: "npm run dev -- --host 0.0.0.0 --port 5173 --strictPort", want: true},
		{name: "npm start", command: "cd api && npm start", want: true},
		{name: "node server", command: "node server.js", want: true},
		{name: "vite direct", command: "vite --host 0.0.0.0", want: true},
		{name: "install then dev", command: "cd /repo/ui && npm install && npm run dev -- --host 0.0.0.0 --port 4173 --strictPort", want: true},
		{name: "next dev", command: "next dev -p 3000", want: true},
		{name: "vite scaffold", command: "npm create vite@latest todo-app -- --template react", want: false},
		{name: "vite build", command: "npm run build", want: false},
		{name: "npm test", command: "npm test -- --watch=false", want: false},
		{name: "already nohup", command: "nohup npm run dev -- --host 0.0.0.0 >/tmp/dev.log 2>&1 &", want: false},
		{name: "already background", command: "npm run dev -- --host 0.0.0.0 &", want: false},
		{name: "short command", command: "npm install", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := shouldAutoDetachDevServer(tt.command)
			if got != tt.want {
				t.Fatalf("shouldAutoDetachDevServer(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestExtractPortFromCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		command string
		want    int
	}{
		{command: "npm run dev -- --host 0.0.0.0 --port 5174 --strictPort", want: 5174},
		{command: "next dev -p 3000", want: 3000},
		{command: "node server.js", want: 0},
	}

	for _, tt := range tests {
		got := extractPortFromCommand(tt.command)
		if got != tt.want {
			t.Fatalf("extractPortFromCommand(%q) = %d, want %d", tt.command, got, tt.want)
		}
	}
}

func TestExtractLogPathFromCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		command string
		want    string
	}{
		{command: "nohup npm run dev -- --host 0.0.0.0 >/tmp/vite.log 2>&1 &", want: "/tmp/vite.log"},
		{command: "npm run dev", want: ""},
	}

	for _, tt := range tests {
		got := extractLogPathFromCommand(tt.command)
		if got != tt.want {
			t.Fatalf("extractLogPathFromCommand(%q) = %q, want %q", tt.command, got, tt.want)
		}
	}
}

func TestEscapeForSingleQuotedShell(t *testing.T) {
	t.Parallel()

	in := "echo 'hello' && npm run dev"
	got := escapeForSingleQuotedShell(in)
	// Expect classic shell escape pattern: '"'"'
	if !strings.Contains(got, `'"'"'`) {
		t.Fatalf("escaped string does not contain expected shell quote escape pattern: %q", got)
	}
	if strings.Count(got, `'"'"'`) != strings.Count(in, "'") {
		t.Fatalf("escaped quote count mismatch: in=%q got=%q", in, got)
	}
}

func TestShellRemediationHints(t *testing.T) {
	t.Parallel()

	result := &sandbox.ExecResult{
		ExitCode: 1,
		Stderr:   "ReferenceError: require is not defined in ES module scope",
	}
	hints := shellRemediationHints("node server.js", result)
	if len(hints) == 0 {
		t.Fatalf("expected remediation hints, got none")
	}

	foundESM := false
	for _, h := range hints {
		if strings.Contains(strings.ToLower(h), "esm") || strings.Contains(strings.ToLower(h), ".cjs") {
			foundESM = true
			break
		}
	}
	if !foundESM {
		t.Fatalf("expected ESM/CJS hint, got: %v", hints)
	}
}

func TestIsManualGitCloneBlocked(t *testing.T) {
	t.Parallel()

	cfgWithRepo := sandbox.SandboxConfig{
		GitRepoURL: "everstacklabs/model-catalog",
		GitBranch:  "master",
	}
	cfgNoRepo := sandbox.SandboxConfig{}

	tests := []struct {
		name     string
		cfg      sandbox.SandboxConfig
		language string
		command  string
		want     bool
	}{
		{
			name:     "blocked when repo preconfigured and git clone in shell command",
			cfg:      cfgWithRepo,
			language: "bash",
			command:  "cd /workspace && git clone https://github.com/foo/bar.git",
			want:     true,
		},
		{
			name:     "blocked when repo preconfigured and uppercase command",
			cfg:      cfgWithRepo,
			language: "sh",
			command:  "GIT   CLONE owner/repo",
			want:     true,
		},
		{
			name:     "not blocked without preconfigured repo",
			cfg:      cfgNoRepo,
			language: "bash",
			command:  "git clone owner/repo",
			want:     false,
		},
		{
			name:     "not blocked for non-shell language",
			cfg:      cfgWithRepo,
			language: "python",
			command:  "git clone owner/repo",
			want:     false,
		},
		{
			name:     "not blocked for non-clone git command",
			cfg:      cfgWithRepo,
			language: "bash",
			command:  "git status",
			want:     false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isManualGitCloneBlocked(tt.cfg, tt.language, tt.command)
			if got != tt.want {
				t.Fatalf("isManualGitCloneBlocked(%q, %q, %q) = %v, want %v", tt.cfg.GitRepoURL, tt.language, tt.command, got, tt.want)
			}
		})
	}
}

func TestPathRequiresRepo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want bool
	}{
		{path: "/repo", want: true},
		{path: "/repo/src/main.go", want: true},
		{path: "/workspace/repo", want: false},
		{path: "/tmp/repo", want: false},
	}

	for _, tt := range tests {
		got := pathRequiresRepo(tt.path)
		if got != tt.want {
			t.Fatalf("pathRequiresRepo(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestShouldEnsureRepoForShellCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		workDir string
		want    bool
	}{
		{
			name:    "repo path in command",
			command: "cd /repo && ls -la",
			workDir: "/workspace",
			want:    true,
		},
		{
			name:    "git command",
			command: "git status",
			workDir: "/workspace",
			want:    true,
		},
		{
			name:    "git version command does not require repo",
			command: "git --version",
			workDir: "/workspace",
			want:    false,
		},
		{
			name:    "git global config does not require repo",
			command: "git config --global user.name \"Agent\"",
			workDir: "/workspace",
			want:    false,
		},
		{
			name:    "repo working directory",
			command: "ls -la",
			workDir: "/repo",
			want:    true,
		},
		{
			name:    "git help does not require repo",
			command: "git help commit",
			workDir: "/workspace",
			want:    false,
		},
		{
			name:    "non repo command",
			command: "ls -la /workspace",
			workDir: "/workspace",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldEnsureRepoForShellCommand(tt.command, tt.workDir)
			if got != tt.want {
				t.Fatalf("shouldEnsureRepoForShellCommand(%q, %q) = %v, want %v", tt.command, tt.workDir, got, tt.want)
			}
		})
	}
}
