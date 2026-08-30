package tools

import (
	"strings"
	"testing"
)

func TestValidateWritablePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		wantErr bool
		errMsg  string
	}{
		{name: "trooper path ok", path: "/workspace/foo.py", wantErr: false},
		{name: "trooper root ok", path: "/workspace", wantErr: false},
		{name: "tmp path ok", path: "/tmp/patch.diff", wantErr: false},
		{name: "repo path rejected", path: "/repo/main.go", wantErr: true, errMsg: "read-only"},
		{name: "repo root rejected", path: "/repo", wantErr: true, errMsg: "read-only"},
		{name: "etc path rejected", path: "/etc/passwd", wantErr: true, errMsg: "must be under"},
		{name: "empty path rejected", path: "", wantErr: true, errMsg: "required"},
		{name: "relative path rejected", path: "foo/bar.py", wantErr: true, errMsg: "must be under"},
		{name: "traversal rejected", path: "/workspace/../etc/passwd", wantErr: true, errMsg: "must be under"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := validateWritablePath(tt.path)
			if tt.wantErr {
				if result == "" {
					t.Fatalf("validateWritablePath(%q) returned no error, want error containing %q", tt.path, tt.errMsg)
				}
				if !strings.Contains(result, tt.errMsg) {
					t.Fatalf("validateWritablePath(%q) = %q, want error containing %q", tt.path, result, tt.errMsg)
				}
			} else {
				if result != "" {
					t.Fatalf("validateWritablePath(%q) = %q, want no error", tt.path, result)
				}
			}
		})
	}
}

func TestValidateWritablePathWithConstraint(t *testing.T) {
	t.Parallel()

	if got := validateWritablePathWithConstraint("/workspace/app/main.go", "/workspace/app"); got != "" {
		t.Fatalf("expected path to be allowed, got error: %s", got)
	}
	if got := validateWritablePathWithConstraint("/workspace/other/main.go", "/workspace/app"); !strings.Contains(got, "working_directory") {
		t.Fatalf("expected working_directory violation, got: %q", got)
	}
}

func TestValidateReadablePathWithConstraint(t *testing.T) {
	t.Parallel()

	if got := validateReadablePathWithConstraint("/repo/project/README.md", "/repo/project"); got != "" {
		t.Fatalf("expected repo path to be allowed, got error: %s", got)
	}
	if got := validateReadablePathWithConstraint("/workspace/project/README.md", "/repo/project"); !strings.Contains(got, "working_directory") {
		t.Fatalf("expected working_directory violation, got: %q", got)
	}
}

func TestSanitizeGlobPattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
		wantErr bool
	}{
		{name: "simple glob", pattern: "*.ts", wantErr: false},
		{name: "deep glob", pattern: "**/*.py", wantErr: false},
		{name: "config glob", pattern: "*.config.js", wantErr: false},
		{name: "bracket glob", pattern: "[a-z]*.go", wantErr: false},
		{name: "hyphen in name", pattern: "my-file.ts", wantErr: false},
		{name: "underscore", pattern: "__init__.py", wantErr: false},
		{name: "brace glob", pattern: "*.{ts,tsx}", wantErr: false},
		{name: "empty rejected", pattern: "", wantErr: true},
		{name: "semicolon rejected", pattern: "*.ts;rm -rf /", wantErr: true},
		{name: "pipe rejected", pattern: "*.ts|cat /etc/passwd", wantErr: true},
		{name: "backtick rejected", pattern: "`whoami`", wantErr: true},
		{name: "dollar rejected", pattern: "$HOME/*.ts", wantErr: true},
		{name: "space rejected", pattern: "foo bar.ts", wantErr: true},
		{name: "ampersand rejected", pattern: "*.ts && echo", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := sanitizeGlobPattern(tt.pattern)
			if tt.wantErr && err == nil {
				t.Fatalf("sanitizeGlobPattern(%q) = nil, want error", tt.pattern)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("sanitizeGlobPattern(%q) = %v, want nil", tt.pattern, err)
			}
		})
	}
}

func TestBuildGrepCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		pattern      string
		path         string
		include      string
		contextLines int
		maxResults   int
		wantContains []string
	}{
		{
			name:         "basic grep",
			pattern:      "func main",
			path:         "/workspace",
			wantContains: []string{"grep", "-rn", "-E", "--", "func main", "/workspace"},
		},
		{
			name:         "with include filter",
			pattern:      "import",
			path:         "/workspace",
			include:      "*.py",
			wantContains: []string{"--include=*.py"},
		},
		{
			name:         "with context lines",
			pattern:      "TODO",
			path:         "/workspace",
			contextLines: 3,
			wantContains: []string{"-C3"},
		},
		{
			name:         "with max results",
			pattern:      "error",
			path:         "/workspace",
			maxResults:   100,
			wantContains: []string{"-m100"},
		},
		{
			name:         "excludes default dirs",
			pattern:      "test",
			path:         "/workspace",
			wantContains: []string{"--exclude-dir=node_modules", "--exclude-dir=.git", "--exclude-dir=__pycache__"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := buildGrepCommand(tt.pattern, tt.path, tt.include, tt.contextLines, tt.maxResults)
			cmdStr := strings.Join(cmd, " ")
			for _, want := range tt.wantContains {
				if !strings.Contains(cmdStr, want) {
					t.Errorf("buildGrepCommand() = %q, want to contain %q", cmdStr, want)
				}
			}
		})
	}
}

func TestBuildFindCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		pattern      string
		path         string
		maxResults   int
		wantContains []string
	}{
		{
			name:         "simple name pattern",
			pattern:      "*.ts",
			path:         "/workspace",
			maxResults:   100,
			wantContains: []string{"find", "/workspace", "-name", "*.ts", "-type", "f"},
		},
		{
			name:         "deep glob strips prefix",
			pattern:      "**/*.py",
			path:         "/workspace",
			maxResults:   100,
			wantContains: []string{"-name", "*.py"},
		},
		{
			name:         "prunes excluded dirs",
			pattern:      "*.go",
			path:         "/workspace",
			maxResults:   100,
			wantContains: []string{"-prune"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := buildFindCommand(tt.pattern, tt.path, tt.maxResults)
			cmdStr := strings.Join(cmd, " ")
			for _, want := range tt.wantContains {
				if !strings.Contains(cmdStr, want) {
					t.Errorf("buildFindCommand(%q, %q, %d) = %q, want to contain %q", tt.pattern, tt.path, tt.maxResults, cmdStr, want)
				}
			}
		})
	}
}

func TestEditReplacementLogic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		content    string
		oldString  string
		newString  string
		replaceAll bool
		wantResult string
		wantErr    bool
	}{
		{
			name:       "single match replace",
			content:    "hello world",
			oldString:  "world",
			newString:  "go",
			replaceAll: false,
			wantResult: "hello go",
		},
		{
			name:       "replace all occurrences",
			content:    "foo bar foo baz foo",
			oldString:  "foo",
			newString:  "qux",
			replaceAll: true,
			wantResult: "qux bar qux baz qux",
		},
		{
			name:       "delete via empty new_string",
			content:    "remove this word please",
			oldString:  "this ",
			newString:  "",
			replaceAll: false,
			wantResult: "remove word please",
		},
		{
			name:       "no match is error",
			content:    "hello world",
			oldString:  "missing",
			newString:  "replacement",
			replaceAll: false,
			wantErr:    true,
		},
		{
			name:       "ambiguous match without replace_all is error",
			content:    "foo bar foo",
			oldString:  "foo",
			newString:  "baz",
			replaceAll: false,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			count := strings.Count(tt.content, tt.oldString)
			if count == 0 {
				if !tt.wantErr {
					t.Fatalf("expected no error but old_string not found")
				}
				return // Error case: not found
			}
			if count > 1 && !tt.replaceAll {
				if !tt.wantErr {
					t.Fatalf("expected no error but got ambiguous match")
				}
				return // Error case: ambiguous
			}

			var result string
			if tt.replaceAll {
				result = strings.ReplaceAll(tt.content, tt.oldString, tt.newString)
			} else {
				result = strings.Replace(tt.content, tt.oldString, tt.newString, 1)
			}

			if result != tt.wantResult {
				t.Fatalf("replacement result = %q, want %q", result, tt.wantResult)
			}
		})
	}
}

func TestShellJoin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "simple args",
			args: []string{"grep", "-rn", "pattern"},
			want: "'grep' '-rn' 'pattern'",
		},
		{
			name: "args with single quotes",
			args: []string{"echo", "it's"},
			want: `'echo' 'it'"'"'s'`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := shellJoin(tt.args)
			if got != tt.want {
				t.Fatalf("shellJoin(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}
