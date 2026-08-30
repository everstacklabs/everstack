# Everstack local quickstart

This starts the Community Edition gateway and PostgreSQL with pgvector. Telemetry, Redis,
ClickHouse, and sandbox access are intentionally omitted so the first run stays
small and predictable.

## Requirements

- Docker with Compose v2
- 6 GB of available memory for the first source build
- Port `8089` available

## Start

From the repository root. The first build can take several minutes; subsequent
builds reuse Docker caches.

```bash
docker compose -f examples/quickstart/compose.yaml up -d --build
curl --fail --retry 30 --retry-delay 2 http://localhost:8089/debug/healthz
```

Open [http://localhost:8089](http://localhost:8089).

In the dashboard:

1. Add an OpenAI, Anthropic, Google, Ollama, or other provider under
   **Vault → LLM Providers**.
2. Create an Everstack API key under **Vault → API Keys**.
3. Send an OpenAI-compatible request using a model configured for that provider.

```bash
export EVERSTACK_API_KEY="replace-with-your-everstack-api-key"

curl --fail-with-body http://localhost:8089/v1/chat/completions \
  -H 'content-type: application/json' \
  -H "x-mf-api-key: ${EVERSTACK_API_KEY}" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [{"role": "user", "content": "Say hello from Everstack"}]
  }'
```

The provider bills model inference directly when you use your provider key,
and the Everstack model-usage charge is **$0**. Token and provider-cost views
remain available for observability.

## Stop

```bash
docker compose -f examples/quickstart/compose.yaml down
```

Add `--volumes` to remove the local database and Everstack data.

## Troubleshooting

```bash
docker compose -f examples/quickstart/compose.yaml ps
docker compose -f examples/quickstart/compose.yaml logs -f everstack
```

For production, do not reuse the example database password or API-key hash
secret. Enable authentication and TLS, use managed secrets, and configure
backups before exposing the service to a network.
