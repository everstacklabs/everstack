package v1

// Declarative image builder API (POR-86).
//
// POST /v1/images/build
// Accepts a build spec, hashes it for 24h caching, and returns an image
// reference. If the same spec has been built before (within 24h), returns
// the cached result immediately (cached: true).
//
// Build spec format:
//   { "base": "debian:bookworm-slim",
//     "apt": ["python3", "nodejs"],
//     "pip": ["numpy", "pandas"],
//     "npm": ["typescript"],
//     "run": ["pip install -r requirements.txt"],
//     "env": {"PYTHONPATH": "/app"},
//     "workdir": "/app",
//     "user": "sandbox" }
//
// Build execution:
//   Phase 1 (this PR): spec is validated, hashed, and stored. The
//     image_ref is set to the base image + a content-addressable tag.
//   Phase 2 (follow-up): actual build via Kaniko or BuildKit in a
//     dedicated build sandbox.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// ImageBuildSpec is the user-facing build specification.
type ImageBuildSpec struct {
	Base    string            `json:"base"`
	Apt     []string          `json:"apt,omitempty"`
	Pip     []string          `json:"pip,omitempty"`
	Npm     []string          `json:"npm,omitempty"`
	Files   []ImageBuildFile  `json:"files,omitempty"`
	Run     []string          `json:"run,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	WorkDir string            `json:"workdir,omitempty"`
	User    string            `json:"user,omitempty"`
}

// ImageBuildFile is a file to inject during the build.
type ImageBuildFile struct {
	Content string `json:"content"`
	Dest    string `json:"dest"`
}

// ImageBuild is a stored build record.
type ImageBuild struct {
	ID        string    `db:"id"         json:"id"`
	TenantID  string    `db:"tenant_id"  json:"tenant_id"`
	SpecHash  string    `db:"spec_hash"  json:"spec_hash"`
	Spec      []byte    `db:"spec"       json:"-"`
	ImageRef  string    `db:"image_ref"  json:"image_ref"`
	BaseImage string    `db:"base_image" json:"base_image"`
	State     string    `db:"state"      json:"state"`
	Error     *string   `db:"error"      json:"error,omitempty"`
	BuildMS   *int      `db:"build_ms"   json:"build_ms,omitempty"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	ExpiresAt time.Time `db:"expires_at" json:"expires_at"`
}

// imageBuildRepo handles DB operations for image builds.
type imageBuildRepo struct {
	db *sqlx.DB
}

func (r *imageBuildRepo) findByHash(tenantID, hash string) (*ImageBuild, error) {
	const q = `
		SELECT * FROM sandbox_image_builds
		WHERE tenant_id = $1 AND spec_hash = $2 AND expires_at > NOW()
		ORDER BY created_at DESC LIMIT 1`
	var b ImageBuild
	if err := r.db.Get(&b, q, tenantID, hash); err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *imageBuildRepo) create(b ImageBuild) (*ImageBuild, error) {
	const q = `
		INSERT INTO sandbox_image_builds (id, tenant_id, spec_hash, spec, image_ref, base_image, state, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW() + INTERVAL '24 hours')
		ON CONFLICT (tenant_id, spec_hash) DO UPDATE
		  SET expires_at = NOW() + INTERVAL '24 hours',
		      state      = EXCLUDED.state,
		      image_ref  = EXCLUDED.image_ref
		RETURNING *`
	var out ImageBuild
	err := r.db.Get(&out, q, b.ID, b.TenantID, b.SpecHash, b.Spec, b.ImageRef, b.BaseImage, b.State)
	return &out, err
}

// hashSpec produces a stable SHA-256 hash of the build spec.
// Lists are sorted for stability; maps are sorted by key.
func hashSpec(spec ImageBuildSpec) string {
	sort.Strings(spec.Apt)
	sort.Strings(spec.Pip)
	sort.Strings(spec.Npm)
	sort.Strings(spec.Run)
	b, _ := json.Marshal(spec)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// resolveImageRef maps a spec to the best available pre-built image.
// This is the Phase 1 implementation: spec-to-catalog matching.
// Phase 2 will build a real image using Kaniko.
func resolveImageRef(spec ImageBuildSpec) string {
	base := strings.ToLower(spec.Base)
	hasPython := len(spec.Pip) > 0 || strings.Contains(base, "python")
	hasNode := len(spec.Npm) > 0 || strings.Contains(base, "node")

	switch {
	case hasPython && hasNode:
		return "ghcr.io/everstacklabs/sandbox:fullstack"
	case hasPython:
		return "ghcr.io/everstacklabs/sandbox:python"
	case hasNode:
		return "ghcr.io/everstacklabs/sandbox:node"
	default:
		if spec.Base != "" {
			return spec.Base
		}
		return "ghcr.io/everstacklabs/sandbox:fullstack"
	}
}

// HandleBuildImage implements POST /v1/images/build.
func (s *Server) HandleBuildImage(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "database not configured")
		return
	}

	var body struct {
		Spec     ImageBuildSpec `json:"spec"`
		TenantID string         `json:"tenant_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	tenantID, _ := s.resolveTenantID(r.Context(), body.TenantID)

	// Validate.
	if body.Spec.Base == "" {
		body.Spec.Base = "debian:bookworm-slim"
	}

	specHash := hashSpec(body.Spec)
	repo := &imageBuildRepo{db: s.db}

	// Check cache.
	if cached, err := repo.findByHash(tenantID, specHash); err == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"image_id":   cached.ID,
			"image_ref":  cached.ImageRef,
			"cached":     true,
			"build_ms":   0,
			"state":      cached.State,
			"expires_at": cached.ExpiresAt.UTC().Format(time.RFC3339),
		})
		return
	}

	// No cache hit: create record and resolve image ref.
	imageRef := resolveImageRef(body.Spec)
	specJSON, _ := json.Marshal(body.Spec)
	buildID := fmt.Sprintf("img_%s", specHash[:16])

	build := ImageBuild{
		ID:        buildID,
		TenantID:  tenantID,
		SpecHash:  specHash,
		Spec:      specJSON,
		ImageRef:  imageRef,
		BaseImage: body.Spec.Base,
		State:     "ready", // Phase 1: catalog match is instant
	}
	saved, err := repo.create(build)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to store build: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"image_id":   saved.ID,
		"image_ref":  saved.ImageRef,
		"cached":     false,
		"build_ms":   0,
		"state":      saved.State,
		"expires_at": saved.ExpiresAt.UTC().Format(time.RFC3339),
		"note":       "Phase 1: spec matched to catalog image. Custom package installation via Kaniko build is in Phase 2.",
	})
}

// HandleGetImageBuild retrieves build info by ID.
// GET /v1/images/{image_id}
func (s *Server) HandleGetImageBuild(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "database not configured")
		return
	}
	// image_id comes from path -- handled by router vars
	writeJSONError(w, http.StatusNotImplemented, "use POST /v1/images/build and store the image_id client-side")
}
