# Everstack local quickstart

This Compose file builds the Community Edition from this repository and runs it
on your machine for evaluation. Nothing is pulled from a private registry: the
only images downloaded are the public `pgvector/pgvector:pg16` database image
and the public base images the build uses.

## What it starts

| Service | What it is | Port |
| --- | --- | --- |
| `everstack` | Gateway, API and the embedded admin dashboard, built from `build/Dockerfile` with the `ce` build tag | `127.0.0.1:8089` |
| `postgres` | PostgreSQL 16 with pgvector, for platform state | not published |

The gateway applies its database migrations when it starts. There is no
separate migration step.

Sign-in is on (`EVS_AUTH_MODE=builtin`): the first account you register in the
dashboard becomes the instance owner. The port is still published on
`127.0.0.1` only, because this setup uses plain HTTP and example secrets. Do not
expose it to a network.

## What it leaves out

These are left out on purpose so the first run stays small. Each one changes
what you can see in the dashboard.

- **ClickHouse.** The gateway runs with `EVS_DATABASE_MODE=single` (PostgreSQL
  only). Trace and log storage needs ClickHouse, so the trace views stay empty
  in this setup. See [Adding tracing](#adding-tracing).
- **Telemetry.** It is switched off: `EVS_TELEMETRY_ENABLED` is `false`.
- **Redis.** The cache is in memory (`EVS_CACHE_TYPE=memory`).
- **Sandboxes.** The container has no access to a Docker daemon, so sandboxes
  and isolated functions are not available. See [Sandboxes](#sandboxes).

## Requirements

- Docker with Compose v2
- About 6 GB of memory for the first source build
- Port `8089` free on `127.0.0.1`

## Start

From the repository root. The first build takes several minutes; later builds
reuse Docker's cache.

```bash
docker compose -f examples/quickstart/compose.yaml up -d --build
curl --fail --retry 30 --retry-delay 2 --retry-all-errors http://localhost:8089/debug/healthz
```

Open [http://localhost:8089](http://localhost:8089). In the dashboard:

1. Register the first account. It becomes the instance owner.
2. Add an OpenAI, Anthropic, Google, Ollama or other provider key under
   **Vault → LLM Providers**.
3. Create an Everstack API key under **Vault → API Keys**.
4. Send an OpenAI-compatible request with a model that provider serves.

The API needs an Everstack API key, and you create the first one from the
signed-in dashboard. Until a provider is configured, `GET /v1/models`
returns an empty list and a chat completion returns `404` with
`model not found`.

The gateway accepts the Everstack API key in the `x-evs-api-key` header:

```bash
export EVERSTACK_API_KEY="replace-with-your-everstack-api-key"

curl --fail-with-body http://localhost:8089/v1/chat/completions \
  -H 'content-type: application/json' \
  -H "x-evs-api-key: ${EVERSTACK_API_KEY}" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [{"role": "user", "content": "Say hello from Everstack"}]
  }'
```

The OpenAI-compatible endpoints under `/v1` also accept it as a bearer token,
so OpenAI client libraries work with only a base URL change:

```bash
curl --fail-with-body http://localhost:8089/v1/chat/completions \
  -H 'content-type: application/json' \
  -H "Authorization: Bearer ${EVERSTACK_API_KEY}" \
  -d '{"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "Hello"}]}'
```

With your own provider key, the provider bills inference directly and the
Everstack model-usage charge is **$0**.

## Adding tracing

Traces, logs and the observability views are stored in ClickHouse. To enable
them, run a ClickHouse server and start the gateway in hybrid mode:

```yaml
environment:
  EVS_DATABASE_MODE: hybrid
  EVS_CLICKHOUSE_DSN: clickhouse://USER:PASSWORD@clickhouse:9000/everstack
```

In hybrid mode the gateway creates its ClickHouse tables at startup and accepts
OpenTelemetry traces from your own applications over OTLP/HTTP at
`/api/public/otel/v1/traces`, authenticated with an Everstack API key.

This repository does not yet ship a tested Compose file for this setup, and the
pipeline that turns the gateway's own requests into trace rows (an
OpenTelemetry Collector writing to the same ClickHouse database, see
`build/otel-collector-config.yaml`) has not been verified end to end for
self-hosting. Treat it as work in progress and report what you find in
[GitHub Discussions](https://github.com/everstacklabs/everstack/discussions).

## Sandboxes

The sandbox Docker backend starts containers through a Docker daemon, which
means giving the gateway access to that daemon. That is equivalent to root on
the host, so the quickstart does not do it. The default sandbox images are not
yet published to a public registry; their Dockerfiles are in
`internal/sandbox/docker/`. Running sandboxes on a self-hosted install is not
covered by a tested guide yet.

## Stop

```bash
docker compose -f examples/quickstart/compose.yaml down
```

Add `--volumes` to delete the local database and Everstack data.

## Troubleshooting

```bash
docker compose -f examples/quickstart/compose.yaml ps
docker compose -f examples/quickstart/compose.yaml logs -f everstack
```

## Before you run it anywhere else

Do not reuse the example database password or API-key hash secret. Keep
authentication on, use TLS and managed secrets, and set up backups before
exposing the service to a network.
