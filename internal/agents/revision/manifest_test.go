package revision

import (
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/functions/isolation"
)

func TestNewManifestCanonicalizesFilesAndFunctions(t *testing.T) {
	t.Parallel()

	manifest, err := NewManifest([]File{
		{Path: "src/risk.py", Content: []byte("def score(args):\n    return args\n")},
		{Path: "src/customer.ts", Content: []byte("export async function lookup(args) { return args; }\n")},
	}, []Function{
		{Name: "score_risk", Path: "src/risk.py", Export: "score", Runtime: isolation.RuntimePython3},
		{Name: "lookup_customer", Path: "src/customer.ts", Export: "lookup", Runtime: isolation.RuntimeDeno},
	})
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}

	if manifest.Format != FormatV1 {
		t.Fatalf("format = %d, want %d", manifest.Format, FormatV1)
	}
	if len(manifest.Files) != 2 || manifest.Files[0].Path != "src/customer.ts" || manifest.Files[1].Path != "src/risk.py" {
		t.Fatalf("files = %+v, want path-sorted files", manifest.Files)
	}
	if len(manifest.Functions) != 2 || manifest.Functions[0].Name != "lookup_customer" || manifest.Functions[1].Name != "score_risk" {
		t.Fatalf("functions = %+v, want name-sorted functions", manifest.Functions)
	}
	if manifest.Files[0].SHA256 == "" || len(manifest.Digest) != 64 {
		t.Fatalf("manifest digests were not populated: %+v", manifest)
	}

	reordered, err := NewManifest([]File{
		{Path: "src/customer.ts", Content: []byte("export async function lookup(args) { return args; }\n")},
		{Path: "src/risk.py", Content: []byte("def score(args):\n    return args\n")},
	}, []Function{
		{Name: "lookup_customer", Path: "src/customer.ts", Export: "lookup", Runtime: isolation.RuntimeDeno},
		{Name: "score_risk", Path: "src/risk.py", Export: "score", Runtime: isolation.RuntimePython3},
	})
	if err != nil {
		t.Fatalf("NewManifest(reordered) error = %v", err)
	}
	if manifest.Digest != reordered.Digest {
		t.Fatalf("digest changed with declaration order: %s != %s", manifest.Digest, reordered.Digest)
	}
}

func TestNewManifestPreservesEmptyExportsForLegacyFallback(t *testing.T) {
	t.Parallel()

	manifest, err := NewManifest([]File{
		{Path: "abc.ts", Content: []byte("export default () => 1")},
		{Path: "abc.py", Content: []byte("def handler(args): return 1")},
	}, []Function{
		{Name: "typescript_function", Path: "abc.ts", Runtime: isolation.RuntimeDeno},
		{Name: "python_function", Path: "abc.py", Runtime: isolation.RuntimePython3},
	})
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	if manifest.Functions[0].Name != "python_function" || manifest.Functions[0].Export != "" {
		t.Fatalf("python export = %+v, want runtime fallback", manifest.Functions[0])
	}
	if manifest.Functions[1].Name != "typescript_function" || manifest.Functions[1].Export != "" {
		t.Fatalf("typescript export = %+v, want runtime fallback", manifest.Functions[1])
	}
}

func TestNewManifestRejectsUnsafeOrAmbiguousInput(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		files     []File
		functions []Function
		want      string
	}{
		"path traversal": {
			files: []File{{Path: "../secret", Content: []byte("x")}},
			want:  "must be a normalized relative path",
		},
		"absolute path": {
			files: []File{{Path: "/tmp/code.py", Content: []byte("x")}},
			want:  "must be a normalized relative path",
		},
		"reserved runtime path": {
			files: []File{{Path: ".everstack/args.json", Content: []byte("x")}},
			want:  "reserved .everstack runtime directory",
		},
		"duplicate file": {
			files: []File{{Path: "code.py", Content: []byte("x")}, {Path: "code.py", Content: []byte("y")}},
			want:  "duplicate file",
		},
		"missing entrypoint": {
			files:     []File{{Path: "code.py", Content: []byte("x")}},
			functions: []Function{{Name: "run_code", Path: "missing.py", Runtime: isolation.RuntimePython3}},
			want:      "references missing file",
		},
		"runtime extension mismatch": {
			files:     []File{{Path: "code.py", Content: []byte("x")}},
			functions: []Function{{Name: "run_code", Path: "code.py", Runtime: isolation.RuntimeDeno}},
			want:      "does not match runtime",
		},
		"invalid function name": {
			files:     []File{{Path: "code.py", Content: []byte("x")}},
			functions: []Function{{Name: "Run-Code", Path: "code.py", Runtime: isolation.RuntimePython3}},
			want:      "must match",
		},
		"reserved runtime function name": {
			files:     []File{{Path: "code.py", Content: []byte("x")}},
			functions: []Function{{Name: "web_fetch", Path: "code.py", Runtime: isolation.RuntimePython3}},
			want:      "reserved by Agent Runtime",
		},
		"duplicate function": {
			files: []File{{Path: "code.py", Content: []byte("x")}},
			functions: []Function{
				{Name: "run_code", Path: "code.py", Runtime: isolation.RuntimePython3},
				{Name: "run_code", Path: "code.py", Runtime: isolation.RuntimePython3},
			},
			want: "duplicate function",
		},
	}

	for name, tt := range tests {
		name, tt := name, tt
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := NewManifest(tt.files, tt.functions)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewManifest() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestNewManifestDigestChangesWithSourceOrExport(t *testing.T) {
	t.Parallel()

	makeManifest := func(source, export string) *Manifest {
		t.Helper()
		manifest, err := NewManifest(
			[]File{{Path: "code.ts", Content: []byte(source)}},
			[]Function{{Name: "run_code", Path: "code.ts", Export: export, Runtime: isolation.RuntimeDeno}},
		)
		if err != nil {
			t.Fatalf("NewManifest() error = %v", err)
		}
		return manifest
	}

	baseline := makeManifest("export const first = () => 1", "first")
	if baseline.Digest == makeManifest("export const first = () => 2", "first").Digest {
		t.Fatal("source change did not change revision digest")
	}
	if baseline.Digest == makeManifest("export const first = () => 1", "second").Digest {
		t.Fatal("export change did not change revision digest")
	}
}

func TestNewManifestValidatesFunctionParameterSchemas(t *testing.T) {
	t.Parallel()

	files := []File{{Path: "code.py", Content: []byte("def run(args): return args")}}
	valid := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"email": map[string]any{"type": "string", "format": "email"},
		},
		"required":             []any{"email"},
		"additionalProperties": false,
	}
	if _, err := NewManifest(files, []Function{{
		Name: "run_code", Path: "code.py", Runtime: isolation.RuntimePython3, Parameters: valid,
	}}); err != nil {
		t.Fatalf("valid JSON Schema rejected: %v", err)
	}

	tests := map[string]struct {
		parameters map[string]any
		want       string
	}{
		"invalid keyword value": {
			parameters: map[string]any{"type": "object", "required": "email"},
			want:       "valid JSON Schema",
		},
		"non-object root": {
			parameters: map[string]any{"type": "string"},
			want:       "root must describe an object",
		},
		"external reference": {
			parameters: map[string]any{"type": "object", "$ref": "https://schemas.example.com/arguments.json"},
			want:       "external references are not allowed",
		},
	}
	for name, test := range tests {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := NewManifest(files, []Function{{
				Name: "run_code", Path: "code.py", Runtime: isolation.RuntimePython3, Parameters: test.parameters,
			}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewManifest() error = %v, want substring %q", err, test.want)
			}
		})
	}
}
