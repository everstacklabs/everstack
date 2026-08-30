// Package revision defines the immutable source bundle deployed with an agent.
// A revision owns the exact files and exported project functions used by a
// session. It is deliberately separate from tenant-global platform Functions.
package revision

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/agents/toolnames"
	"github.com/everstacklabs/everstack/internal/functions/isolation"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

const (
	// FormatV1 is the first canonical agent source manifest format.
	FormatV1 = 1

	// Upload limits bound database growth and sandbox materialization work.
	MaxFiles       = 512
	MaxFileBytes   = 2 << 20
	MaxTotalBytes  = 8 << 20
	MaxSchemaBytes = 64 << 10
)

var functionNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

// File is one immutable file in an agent revision. Content is stored outside
// the JSON manifest while the digest, mode and size remain in its metadata.
type File struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Mode    int32  `json:"mode"`
	Size    int64  `json:"size"`
	Content []byte `json:"-"`
}

// Function exposes one file export as an LLM-callable project function.
type Function struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Path        string                 `json:"path"`
	Export      string                 `json:"export"`
	Runtime     isolation.Runtime      `json:"runtime"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

// Manifest is the canonical, content-addressed source contract for a revision.
type Manifest struct {
	Format    int        `json:"format"`
	Digest    string     `json:"digest"`
	Files     []File     `json:"files"`
	Functions []Function `json:"functions,omitempty"`
}

// Revision is one stored immutable manifest for an agent.
type Revision struct {
	ID        string    `db:"id" json:"id"`
	TenantID  string    `db:"tenant_id" json:"tenant_id"`
	AgentID   string    `db:"agent_id" json:"agent_id"`
	Number    int       `db:"revision_number" json:"number"`
	Digest    string    `db:"digest" json:"digest"`
	Manifest  Manifest  `json:"manifest"`
	CreatedBy string    `db:"created_by" json:"created_by,omitempty"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// NewManifest validates and canonicalizes a source bundle. It recomputes every
// file digest and the top-level digest, so callers never choose revision IDs by
// supplying untrusted hash metadata.
func NewManifest(files []File, functions []Function) (*Manifest, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("revision must contain at least one file")
	}
	if len(files) > MaxFiles {
		return nil, fmt.Errorf("revision contains %d files; maximum is %d", len(files), MaxFiles)
	}

	canonicalFiles := make([]File, len(files))
	seenFiles := make(map[string]struct{}, len(files))
	var totalBytes int64
	for i, source := range files {
		clean, err := validatePath(source.Path)
		if err != nil {
			return nil, fmt.Errorf("file %q: %w", source.Path, err)
		}
		if _, duplicate := seenFiles[clean]; duplicate {
			return nil, fmt.Errorf("duplicate file %q", clean)
		}
		seenFiles[clean] = struct{}{}

		size := int64(len(source.Content))
		if size > MaxFileBytes {
			return nil, fmt.Errorf("file %q is %d bytes; maximum is %d", clean, size, MaxFileBytes)
		}
		totalBytes += size
		if totalBytes > MaxTotalBytes {
			return nil, fmt.Errorf("revision is %d bytes; maximum is %d", totalBytes, MaxTotalBytes)
		}

		mode := source.Mode
		if mode == 0 {
			mode = 0o644
		}
		if mode < 0 || mode > 0o777 {
			return nil, fmt.Errorf("file %q has invalid mode %#o", clean, mode)
		}
		sum := sha256.Sum256(source.Content)
		canonicalFiles[i] = File{
			Path:    clean,
			SHA256:  hex.EncodeToString(sum[:]),
			Mode:    mode,
			Size:    size,
			Content: append([]byte(nil), source.Content...),
		}
	}
	sort.Slice(canonicalFiles, func(i, j int) bool { return canonicalFiles[i].Path < canonicalFiles[j].Path })

	canonicalFunctions := make([]Function, len(functions))
	seenFunctions := make(map[string]struct{}, len(functions))
	for i, function := range functions {
		function.Name = strings.TrimSpace(function.Name)
		if !functionNameRE.MatchString(function.Name) {
			return nil, fmt.Errorf("function name %q must match %s", function.Name, functionNameRE)
		}
		if toolnames.IsReserved(function.Name) {
			return nil, fmt.Errorf("function name %q is reserved by Agent Runtime", function.Name)
		}
		if _, duplicate := seenFunctions[function.Name]; duplicate {
			return nil, fmt.Errorf("duplicate function %q", function.Name)
		}
		seenFunctions[function.Name] = struct{}{}

		clean, err := validatePath(function.Path)
		if err != nil {
			return nil, fmt.Errorf("function %q path: %w", function.Name, err)
		}
		if _, exists := seenFiles[clean]; !exists {
			return nil, fmt.Errorf("function %q references missing file %q", function.Name, clean)
		}
		if !runtimeMatchesPath(function.Runtime, clean) {
			return nil, fmt.Errorf("function %q file %q does not match runtime %q", function.Name, clean, function.Runtime)
		}

		function.Path = clean
		function.Export = strings.TrimSpace(function.Export)
		if err := validateParameterSchema(function.Parameters); err != nil {
			return nil, fmt.Errorf("function %q parameters: %w", function.Name, err)
		}
		if len(function.Parameters) > 0 {
			function.Parameters = cloneMap(function.Parameters)
		}
		canonicalFunctions[i] = function
	}
	sort.Slice(canonicalFunctions, func(i, j int) bool { return canonicalFunctions[i].Name < canonicalFunctions[j].Name })

	manifest := &Manifest{
		Format:    FormatV1,
		Files:     canonicalFiles,
		Functions: canonicalFunctions,
	}
	digest, err := manifestDigest(manifest)
	if err != nil {
		return nil, err
	}
	manifest.Digest = digest
	return manifest, nil
}

func validateParameterSchema(parameters map[string]interface{}) error {
	if len(parameters) == 0 {
		return nil
	}
	if rootType, ok := parameters["type"].(string); !ok || rootType != "object" {
		return fmt.Errorf("JSON Schema root must describe an object")
	}
	encoded, err := json.Marshal(parameters)
	if err != nil {
		return fmt.Errorf("must be JSON-compatible: %w", err)
	}
	if len(encoded) > MaxSchemaBytes {
		return fmt.Errorf("JSON Schema is %d bytes; maximum is %d", len(encoded), MaxSchemaBytes)
	}

	const resourceURL = "mem://everstack/function-parameters.json"
	compiler := jsonschema.NewCompiler()
	compiler.LoadURL = func(url string) (io.ReadCloser, error) {
		return nil, fmt.Errorf("external references are not allowed: %s", url)
	}
	if err := compiler.AddResource(resourceURL, bytes.NewReader(encoded)); err != nil {
		return fmt.Errorf("must be valid JSON Schema: %w", err)
	}
	if _, err := compiler.Compile(resourceURL); err != nil {
		return fmt.Errorf("must be valid JSON Schema: %w", err)
	}
	return nil
}

// FunctionByName resolves a project-scoped function without consulting the
// tenant-global Functions namespace.
func (m *Manifest) FunctionByName(name string) (Function, bool) {
	if m == nil {
		return Function{}, false
	}
	for _, function := range m.Functions {
		if function.Name == name {
			return function, true
		}
	}
	return Function{}, false
}

func validatePath(raw string) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.Contains(raw, `\`) || strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("must be a normalized relative path")
	}
	clean := path.Clean(raw)
	if clean == "." || clean != raw || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("must be a normalized relative path")
	}
	if clean == ".everstack" || strings.HasPrefix(clean, ".everstack/") {
		return "", fmt.Errorf("uses the reserved .everstack runtime directory")
	}
	return clean, nil
}

func runtimeMatchesPath(runtime isolation.Runtime, filePath string) bool {
	switch runtime {
	case isolation.RuntimeDeno:
		return strings.HasSuffix(filePath, ".ts")
	case isolation.RuntimeNodeJS20:
		return strings.HasSuffix(filePath, ".js") || strings.HasSuffix(filePath, ".mjs")
	case isolation.RuntimePython3:
		return strings.HasSuffix(filePath, ".py")
	default:
		return false
	}
}

func manifestDigest(manifest *Manifest) (string, error) {
	type fileState struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
		Mode   int32  `json:"mode"`
		Size   int64  `json:"size"`
	}
	files := make([]fileState, len(manifest.Files))
	for i, file := range manifest.Files {
		files[i] = fileState{Path: file.Path, SHA256: file.SHA256, Mode: file.Mode, Size: file.Size}
	}
	state := struct {
		Format    int         `json:"format"`
		Files     []fileState `json:"files"`
		Functions []Function  `json:"functions,omitempty"`
	}{Format: manifest.Format, Files: files, Functions: manifest.Functions}
	encoded, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("encode revision manifest: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func cloneMap(source map[string]interface{}) map[string]interface{} {
	encoded, _ := json.Marshal(source)
	var cloned map[string]interface{}
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}
