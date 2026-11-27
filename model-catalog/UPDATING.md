# Updating the Model Catalog

The hierarchical files under `model-catalog/providers/` are the source of
truth. Do not edit the generated embedded `models.yaml` or `providers.yaml`
files by hand.

## Add or update models

1. Edit the relevant files under
   `model-catalog/providers/<provider>/models/`.
2. Include an ISO `release_date` so provider sheets can sort newest-first.
3. Keep model IDs compatible with the provider adapter. Meta-providers may
   keep a curated static fallback when their full inventory is discovered at
   runtime.
4. Add a structured entry to `model-catalog/changelog.yaml`.
5. Bump `model-catalog/version.txt`:
   - minor for new models or providers;
   - patch for metadata, pricing, or status corrections.

For a catalog-wide refresh from the same models.dev source used by the current
catalog, review the allowlist and run:

```bash
# Dry run: prints the path of every file it would add
node model-catalog/scripts/sync-models-dev.mjs --source /path/to/api.json

# Add the reviewed new models
node model-catalog/scripts/sync-models-dev.mjs \
  --source /path/to/api.json \
  --write
```

By default the script only adds models the catalog does not have. Existing
model files are never rewritten, so hand-verified prices, statuses and
descriptors are safe. Every check that can fail runs before the first file is
written, so an error leaves the catalog untouched.

The script also accepts:

- `--only <provider[,provider...]>` restricts the run (additions, reconcile
  and deprecation) to the listed catalog provider slugs, for example
  `--only aws-bedrock,vertex-ai`. Unknown slugs are an error.
- `--added-in-version x.y.z` stamps `added_in_version: "x.y.z"` on newly
  written files. Existing files are never stamped.
- `--reconcile` opts in to updating existing files from models.dev (status,
  release date, cost, limits, capabilities, modalities). The dry run lists
  each changed file with its changed top-level keys. Even then:
  - files whose header has a line starting `# Verified` are skipped unless
    `--include-verified` is also passed;
  - `status: "deprecated"` is never changed back;
  - `cost.long_context`, `cost.cache_write_1h_per_1k` (and any other cost
    entry models.dev does not carry) and `unsupported_parameters` are kept
    verbatim; the run aborts rather than drop them.
- `--deprecate-missing` (requires `--reconcile`) marks full-inventory models
  models.dev no longer lists as deprecated.
- `--additions-only` is still accepted; it is now the default behaviour.

Run with `--write` only after reviewing the dry-run output. When you verify a
model file against the provider's own documentation, start its header with
`# Verified <date>.` so a later reconcile leaves it alone.

## Request controls a model rejects

Some models return an error for request controls the gateway otherwise
forwards. List them on the model file, only when the provider documents the
rejection:

```yaml
unsupported_parameters:
  - "temperature"
  - "top_p"
```

Recognised keys (defined in `internal/services/catalog/types.go`, applied by
`internal/providers/modelparams`):

| Key | Request field it clears |
| --- | --- |
| `temperature` | `temperature` |
| `top_p` | `top_p` |
| `top_k` | `top_k` |
| `thinking_toggle` | an explicit thinking enabled or disabled switch |
| `thinking_budget` | a manual reasoning token budget |

Before a chat request (unary or streaming) reaches the provider adapter, the
gateway removes any listed control the caller or a configured default set, and
logs the names it removed at debug level. Unknown keys are ignored. Reasoning
effort and forced tool choice are never removed, because dropping them would
change what the request means. Key matching is case-insensitive. The request's
model is resolved to a catalog entry by exact ID, then without a `[1m]`-style
suffix, then by the longest catalog ID it extends with a `-` (dated
snapshots), so a snapshot ID inherits its base model's list. Set the field on
each provider's copy of a model separately; it is not inherited across
providers. `sync-models-dev.mjs` never writes or changes this field, and its
reconcile pass stops with an error rather than alter it.

## Regenerate artifacts

Run both generators from the repository root:

```bash
model-catalog/scripts/generate-manifest.sh
go run model-catalog/scripts/generate-embedded-defaults.go
```

The manifest drives legacy unsigned sync. The embedded defaults keep offline and
pre-sync gateway behavior aligned with the same catalog.

The manifest is retained for source compatibility. Signed production releases
are built directly from the complete hierarchical directory and do not need
GitHub or raw file-by-file retrieval.

## Verify

At minimum:

```bash
go test ./cmd/config/gateway/validator \
  ./internal/services/provider_catalog \
  ./internal/services/catalog_sync \
  ./internal/api/grpc/catalog/v1 \
  ./internal/api/grpc/providers/v1
```

Also verify that:

- `manifest.yaml` matches `version.txt`;
- manifest model counts match the files on disk;
- the newest dated models appear first in the provider API;
- `changelog.yaml` contains every model intended to receive a “new” marker.

## Release pipeline

Catalog-only changes are released by `.github/workflows/sync-model-catalog.yaml`.
Every job in that workflow is pinned to
`[self-hosted, macOS, X64, docker, catalog, arnabtarwani-mac-2]`. A merge to `master` that changes
`model-catalog/**` performs this sequence:

1. validate the complete source, manifest inventory, statistics, changelog,
   and aggregated catalog;
2. test the signed distribution protocol;
3. verify the embedded offline defaults are current;
4. build and sign one complete immutable bundle;
5. publish and read back the bundle through the R2 S3 API;
6. promote the signed channel with an ETag compare-and-swap;
7. fetch the exact version through `catalog.everstack.ai` and verify its
   signature, contents, ETag, and cache policy;
8. mirror the human-readable source to GitHub as a non-critical follow-up.

The R2 promotion is the release commit point. A GitHub mirror failure does not
undo or invalidate a catalog release.

Configure the `catalog-production` GitHub environment on the self-hosted runner
with:

- variable `CATALOG_R2_ENDPOINT`;
- secrets `CATALOG_R2_ACCESS_KEY_ID` and
  `CATALOG_R2_SECRET_ACCESS_KEY`, scoped only to
  `everstack-catalog-releases`;
- secret `CATALOG_SIGNING_PRIVATE_KEY`.

The signing key and R2 credentials must also live in the independent release
secret manager. GitHub Actions is a normal trigger, not the only release path.

## Publish while GitHub is unavailable

Create signing keys once and keep the private file outside the repository:

```bash
go run ./tools/catalog-release keygen \
  --private-key-file /secure/path/catalog-signing-private.key \
  --public-key-file /secure/path/catalog-signing-public.key
```

Load the private file through the release secret manager and configure the
bucket-scoped `EVS_CATALOG_R2_*` variables on the self-hosted runner. Then run
the same pipeline used by CI on that runner:

```bash
make catalog-validate CATALOG_DIR=/path/to/llm-catalog
make catalog-build CATALOG_DIR=/path/to/llm-catalog
make catalog-publish CATALOG_DIR=/path/to/llm-catalog
```

`catalog-build` is optional during an ordinary publish, but produces the exact
signed artifacts for inspection. `catalog-publish` rebuilds the complete
immutable bundle, publishes it, promotes the selected channel, and verifies the
public domain before returning success. Use `CATALOG_CHANNEL=candidate` for a
candidate release and the default `stable` for production.

The command can also be invoked directly:

```bash
go run ./tools/catalog-release publish \
  --catalog-dir /path/to/llm-catalog \
  --channel stable \
  --public-url https://catalog.everstack.ai/v1
```

The release succeeds only after the immutable bundle and promoted channel have
both been read back byte-for-byte and the public custom domain serves the exact
signed version with its required cache metadata. Reusing a version with
different content is rejected, and channel promotion uses an ETag
compare-and-swap.

The signed production distribution target is:

```text
https://catalog.everstack.ai/v1
```

Production gateways require the matching `EVS_CATALOG_PUBLIC_KEY` and
`EVS_CATALOG_REQUIRE_SIGNATURE=true`. Before changing production
configuration, complete the live verification gate in
`infra/cloudflare/r2-catalog-distribution/README.md`. Without a configured trust
key, remote refresh is rejected and the gateway keeps its local last-known-good
or embedded catalog.
