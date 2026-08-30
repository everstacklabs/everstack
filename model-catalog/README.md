# Model Catalog

This directory contains the source for the centralized model and provider
catalog. Runtime releases are aggregated, signed, and published independently
of the gateway binary and GitHub availability.

## Structure (v2.0)

The catalog uses a hierarchical directory structure for easy maintenance:

```
model-catalog/
├── manifest.yaml           # Lists all files for remote sync
├── version.txt             # Current catalog version
├── changelog.yaml          # Version history
├── providers/
│   ├── openai/
│   │   ├── provider.yaml   # Provider config (auth, rate limits, etc.)
│   │   ├── categories.yaml # Model categories for this provider
│   │   ├── templates.yaml  # Configuration templates
│   │   └── models/
│   │       ├── gpt-4o.yaml
│   │       ├── gpt-4o-mini.yaml
│   │       └── ...
│   ├── anthropic/
│   │   ├── provider.yaml
│   │   ├── categories.yaml
│   │   ├── templates.yaml
│   │   └── models/
│   │       ├── claude-sonnet-4-20250514.yaml
│   │       └── ...
│   ├── cohere/
│   ├── google/
│   ├── mistral/
│   ├── openrouter/
│   ├── huggingface/
│   └── ollama/
└── README.md
```

## File Formats

### Provider Configuration (`provider.yaml`)

```yaml
name: "openai"
display_name: "OpenAI"
base_url: "https://api.openai.com/v1"
api_version: "2024-01-01"

provider_type: "static" # or "meta" for dynamic providers
supports_model_discovery: false

auth:
  type: "api_key"
  header_name: "Authorization"
  header_format: "Bearer {api_key}"
  env_var: "OPENAI_API_KEY"

rate_limits:
  requests_per_minute: 3500
  tokens_per_minute: 90000
  concurrent_requests: 50

capabilities:
  chat: true
  embeddings: true
  vision: true
  # ...
```

### Model Configuration (`models/*.yaml`)

```yaml
name: "gpt-4o"
display_name: "GPT-4o"
family: "gpt-4"
status: "stable"
release_date: "2024-05-13" # ISO 8601; used for newest-first ordering

cost:
  input_per_1k: 0.005
  output_per_1k: 0.015

limits:
  max_tokens: 128000
  max_completion_tokens: 16384

capabilities:
  - chat
  - function_calling
  - vision

modalities:
  input: ["text", "image"]
  output: ["text"]
```

### Categories (`categories.yaml`)

Groups models by capability for the provider:

```yaml
chat:
  - gpt-4o
  - gpt-4o-mini

embeddings:
  - text-embedding-3-large
  - text-embedding-3-small

vision:
  - gpt-4o
  - gpt-4o-mini
```

### Templates (`templates.yaml`)

Pre-configured settings for common use cases:

```yaml
high_performance:
  name: "High Performance"
  description: "Optimized for low latency"
  settings:
    timeout: 30s
    retry_attempts: 2

cost_optimized:
  name: "Cost Optimized"
  recommended_models:
    - gpt-4o-mini
```

## Adding a New Model

1. Navigate to the provider directory: `providers/{provider}/models/`
2. Create a new YAML file (e.g., `new-model.yaml`)
3. Update the version: `version.txt` (minor bump)
4. Add a structured changelog entry: `changelog.yaml`
5. Regenerate the manifest and embedded offline defaults:

   ```bash
   ./scripts/generate-manifest.sh
   go run model-catalog/scripts/generate-embedded-defaults.go
   ```
6. Run `make catalog-validate`. After merge, the self-hosted catalog release
   pipeline publishes and verifies the signed release automatically. The
   gateway applies it asynchronously without a restart or gateway deployment.

If GitHub Actions is unavailable, an operator can run
`make catalog-publish CATALOG_DIR=/path/to/llm-catalog` with credentials loaded
from the independent release secret manager. This is the same release path as
CI, not a separate emergency format.

For a catalog-wide models.dev refresh, use the reviewed allowlist in
`scripts/sync-models-dev.mjs`. It defaults to a dry run:

```bash
node model-catalog/scripts/sync-models-dev.mjs --source /path/to/api.json
node model-catalog/scripts/sync-models-dev.mjs --source /path/to/api.json --write
```

## Adding a New Provider

1. Create provider directory: `mkdir -p providers/{provider}/models`
2. Create `provider.yaml` with provider configuration
3. Create `categories.yaml` and `templates.yaml`
4. Add models to `models/` subdirectory
5. Bump version in `version.txt`
6. Regenerate, validate, and publish the signed catalog release.

## Manifest Generation

The `manifest.yaml` is **auto-generated** - do not edit it manually!

### Local Generation

```bash
./scripts/generate-manifest.sh
```

### GitHub Actions (Source convenience)

The manifest is automatically regenerated when:

- Files in `model-catalog/providers/**` are changed
- `version.txt` is updated
- Pushes to `main` or `dev` branches

The workflow (`.github/workflows/generate-catalog-manifest.yaml`):

1. Scans the `providers/` directory structure
2. Generates `manifest.yaml` with all files listed
3. Commits and pushes the updated manifest

For PRs, it uploads the manifest as an artifact and comments with a preview.

The GitHub workflow keeps source artifacts reviewable. It is not the runtime
distribution path and is not required to publish from a local checkout.

## Runtime Distribution

The catalog is delivered through a signed CodePush-style release protocol:

1. The release tool validates and aggregates the complete hierarchical source.
2. It signs a channel document that identifies the exact immutable bundle.
3. It uploads the bundle to dedicated Cloudflare R2 storage and reads it back.
4. It promotes the small signed channel pointer with an ETag compare-and-swap
   only after bundle verification.
5. Gateways poll the channel asynchronously and retain their last-known-good
   catalog on any failure. Gateways authenticate the signature and then verify
   the exact path, length, SHA-256 digest, schema, version, and contents.

Target production URL:

```
https://catalog.everstack.ai/v1
```

GitHub remains a source collaboration surface. It is not in the production
gateway startup or refresh path after the signed distribution configuration is
rolled out. See `docs/architecture/model-catalog-distribution.md` and
`infra/cloudflare/r2-catalog-distribution/README.md`.

## Version Format

We use semantic versioning:

- **MAJOR** (2.0.0): Breaking structure changes
- **MINOR** (2.1.0): New models or providers added
- **PATCH** (2.0.1): Bug fixes, pricing updates

## Legacy Compatibility

The loader supports both the new hierarchical structure and legacy flat files (`models.yaml`, `providers.yaml`). When loading:

1. First tries `providers/` directory structure
2. Falls back to `models.yaml` + `providers.yaml` if not found

This ensures backward compatibility during the source-layout transition. The
unsigned manifest and flat-file remote loaders also remain temporarily for a
staged migration from custom legacy origins when operators explicitly set
`EVS_CATALOG_REQUIRE_SIGNATURE=false`. The official R2 origin has no unsigned
mode. New production releases use signed bundles, and gateways require
signature verification by default.
